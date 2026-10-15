# Wildcard certificates through Hetzner and Cloudflare DNS

Research for [#4](https://github.com/hyprsh/tnl/issues/4) (map [#1](https://github.com/hyprsh/tnl/issues/1)). State checked on 2026-10-15.

Vocabulary: **Zone**, **Edge** and **Hostname** are tnl terms from `GLOSSARY.md`. "DNS zone" means the zone object held by the DNS provider (Hetzner or Cloudflare). The two often differ: a tnl Zone `tnl.example.com` lives inside the DNS zone `example.com`.

Versions examined: Caddy [v2.11.7](https://github.com/caddyserver/caddy/releases/tag/v2.11.7) (2026-10-03), certmagic [v0.25.6](https://github.com/caddyserver/certmagic/releases/tag/v0.25.6) (2026-09-30), libdns [v1.1.1](https://github.com/libdns/libdns/releases/tag/v1.1.1), caddy-dns/hetzner [v2.0.1](https://github.com/caddy-dns/hetzner/releases/tag/v2.0.1), libdns/hetzner [v2.0.1](https://github.com/libdns/hetzner/releases/tag/v2.0.1), caddy-dns/cloudflare [v0.2.4](https://github.com/caddy-dns/cloudflare/tree/v0.2.4), libdns/cloudflare [v0.2.2](https://github.com/libdns/cloudflare/tree/v0.2.2).

## Answer

- **Hetzner: only the new Cloud DNS API works now, and only caddy-dns/hetzner v2 supports it.** Hetzner switched the legacy DNS Console API to read-only on 20 May 2026 and removed it on 27 May 2026. `dns.hetzner.com` now redirects to `console.hetzner.com`. `github.com/caddy-dns/hetzner/v2` (v2.0.0 from 2026-04-04, v2.0.1 from 2026-05-28) wraps `libdns/hetzner/v2`, which uses the Cloud API (`api.hetzner.cloud/v1/zones`) through `hcloud-go`. v1.x of both packages targets the removed API and is dead. One trap remains: Caddy's plugin registry and download page still list only the v1 module path ([§1](#1-caddy-dnshetzner-and-hetzners-api-cutover)).
- **Cloudflare: caddy-dns/cloudflare is current and maintained.** v0.2.4 (2026-03-23) wraps `libdns/cloudflare` v0.2.2 on the libdns 1.x API. v0.2.4 is the first release that accepts Cloudflare's new `cfut_`/`cfat_` token format; v0.2.3 rejects those tokens. Open weak spots: the module puts the full token into its validation error message (PR #132 is open), and libdns/cloudflare has no retry or rate-limit handling ([§2](#2-caddy-dnscloudflare)).
- **libdns interface: small, and ACME needs only two methods.** libdns defines `RecordGetter`, `RecordAppender`, `RecordSetter`, `RecordDeleter` and the optional `ZoneLister`. certmagic's DNS-01 solver needs only `AppendRecords` and `DeleteRecords` (`certmagic.DNSProvider`). Caddy's global `tls.dns` default additionally requires get and set. Adding a provider takes two pieces:
  - a Go package implementing those methods (there is a template repo and a list of acceptance rules);
  - about 70 lines of caddy-dns wrapper that registers `dns.providers.<name>`.

  About 100 libdns providers and 99 caddy-dns modules already exist ([§3](#3-libdns-the-interface-and-what-a-new-provider-takes)).
- **Minimum token scopes:**
  - **Cloudflare:** one API token with *Zone · Zone · Read* plus *Zone · DNS · Edit*. Its zone resources can be limited to the specific DNS zone, and IP filtering and an expiry can be added.
  - **Hetzner:** a Cloud API token with *Read & Write*, created in the Hetzner Project that holds the DNS zone. No finer scope exists, so the token can also write everything else in that Project, servers included. Hetzner's own advice is to put DNS zones in a separate Project ([§4](#4-minimum-token-scopes)).
- **How modules get compiled in:** Caddy has no runtime plugin loading. A DNS module's `init()` registers it when its package is imported.
  - **Stock Caddy:** `xcaddy build --with …`, `caddy add-package …`, or the download page.
  - **Embedded Caddy:** tnl's own Go module adds blank imports (`_ "github.com/caddy-dns/cloudflare"`, `_ "github.com/caddy-dns/hetzner/v2"`).

  Each module ID can be registered only once; a duplicate panics. That means hetzner v1 and v2 cannot both be linked ([§5](#5-compiling-dns-modules-in)).
- **Per-Zone provider choice at runtime works, but only among the providers compiled in.** In Caddy's TLS app, each automation policy has its own `subjects` filter and its own ACME issuer, and that issuer has its own `challenges.dns.provider` (`{"name": "hetzner", "api_token": …}`). Policies are matched first-match by subject, wildcards included. So one config can hold one policy per wildcard Zone, each with a different provider and token. Changing a Zone's provider or token is a config change, which reloads the whole Caddy config gracefully and rolls back on failure ([§6](#6-choosing-the-provider-per-zone-at-runtime)).
- **Wildcard behaviour in Caddy ≥ 2.10:** when a wildcard certificate is managed, Caddy uses it for the matching subdomain Hostnames instead of issuing individual certificates, unless a name is listed explicitly under `automate`. A wildcard covers exactly one label. It does not cover the apex (`example.com`) or deeper names (`a.b.example.com`) ([§7](#7-cross-cutting-facts-that-shape-per-zone-wildcards)).

### Implications for tnl

These follow from the facts above. They are inputs for [#8 Zones and certificate modes](https://github.com/hyprsh/tnl/issues/8), not decisions.

- **A Zone's DNS settings can be plain data.** A wildcard Zone maps to one Caddy automation policy (subjects `*.<zone>`, plus the apex if wanted) with its own DNS challenge config; per-Hostname Zones get policies without one. Several Zones with different providers, or different accounts of the same provider, coexist in one config. Because policies are first-match, the generated policy order matters.
- **There are three places to put the pluggability seam:**
  1. Reuse caddy-dns modules as they are: blank imports plus a tnl-side mapping from provider name to Caddy module ID and JSON fields. Adding a provider means one import and one config-and-validation entry.
  2. Register one tnl-owned module in the `dns.providers` namespace that hands calls to libdns providers chosen per Zone. tnl would then own validation, secret handling and error redaction, and would not inherit wrapper quirks like the Cloudflare token regex or the Hetzner Caddyfile parser.
  3. Skip Caddy's ACME issuer and drive certmagic with libdns providers directly.

  In every option, the set of providers is fixed when the `tnl` binary is built.
- **The provider's DNS zone can differ from the tnl Zone.** certmagic finds the DNS zone with an SOA lookup, and Hetzner does not support subdomain zones. A Zone `tnl.example.com` therefore needs a token that can write `example.com`.
- **Credentials end up in places tnl may not expect.** Tokens placed in Caddy JSON are autosaved to disk (`autosave.json`, mode 0600) unless `admin.config.persist` is false, and the admin API returns them from `GET /config/`. caddy-dns modules resolve `{env.*}` placeholders when they provision.
- **Bad credentials can fail in unhelpful ways.** libdns/hetzner panics on an empty token (certmagic's job worker recovers the panic). caddy-dns/cloudflare rejects tokens its regex does not recognise and echoes the token in the error. tnl may want to check credentials before handing them to Caddy.
- **A Hetzner token is the riskiest credential in v1.** If the Edge runs on Hetzner Cloud in the same Project as the DNS zone, a token leaked from the Edge can also delete the Edge's own server.

---

## 1. caddy-dns/hetzner and Hetzner's API cutover

### Hetzner timeline (primary: Hetzner status page and docs)

| Date | Event | Source |
| --- | --- | --- |
| 2025-10-07 | New DNS API (part of the Hetzner Cloud API) enters beta | [Cloud API changelog](https://docs.hetzner.cloud/changelog) |
| 2025-11-10 | DNS in the Hetzner Console is GA; creating zones under dns.hetzner.com is no longer possible | [Cloud API changelog](https://docs.hetzner.cloud/changelog) |
| 2026-02-16 → 2026-05-20 | Scheduled "brownouts" of the old DNS API (HTTP 503: "Temporary Shutdown (Brownout) for planned API shutdown on 20. May 2026") | [Hetzner DNS FAQ, archived 2026-04-13](http://web.archive.org/web/20260413165627/https://docs.hetzner.com/networking/dns/faq/beta/) |
| April 2026 | Automatic migration of all remaining zones into the Hetzner Console | same FAQ |
| 2026-05-18 | "The old DNS Console & API will be moved to a read-only mode as of 20.05. As of now, no zones are available in the old system anymore." | [status.hetzner.com incident](https://status.hetzner.com/incident/c2146c42-6dd2-4454-916a-19f07e0e5a44) |
| 2026-05-27 | Resolved: "The DNS console and API was removed. dns.hetzner.com redirects as of now to console.hetzner.com" | same incident |

Checked on 2026-10-15: `https://dns.hetzner.com/api/v1/zones` answers `301` to `https://console.hetzner.com/`. That explains the `invalid character '<' looking for beginning of value` errors users of v1 report ([caddy-dns/hetzner#32](https://github.com/caddy-dns/hetzner/issues/32)). The original FAQ URL now returns 404, so the archived copy is cited.

Tokens from the old DNS Console do not work with the Cloud API ([Hetzner: features and differences](https://docs.hetzner.com/networking/dns/migration-to-hetzner-console/features-and-differences)).

### Module and library state

- **`github.com/caddy-dns/hetzner/v2`**: [v2.0.0](https://github.com/caddy-dns/hetzner/releases/tag/v2.0.0) (2026-04-04: "Support for the new Hetzner Cloud DNS API and the latest Caddy version") and [v2.0.1](https://github.com/caddy-dns/hetzner/releases/tag/v2.0.1) (2026-05-28). Previews 1–3 date from Nov–Dec 2025. Its [go.mod](https://github.com/caddy-dns/hetzner/blob/v2.0.1/go.mod) requires `caddy/v2 v2.11.2` and `libdns/hetzner/v2 v2.0.1`. Module ID: `dns.providers.hetzner`. The only config field is `api_token` ([hetzner.go](https://github.com/caddy-dns/hetzner/blob/v2.0.1/hetzner.go)). The README says the module "manages DNS records for the Hetzner Console using the Cloud DNS API" ([README](https://github.com/caddy-dns/hetzner/blob/v2.0.1/README.md)).
- **`github.com/libdns/hetzner/v2`**: [v2.0.0](https://github.com/libdns/hetzner/releases/tag/v2.0.0) (2025-11-06) and [v2.0.1](https://github.com/libdns/hetzner/releases/tag/v2.0.1) (2025-12-13). The README states it plainly: v1 "implements the deprecated Hetzner DNS Console API", v2 "implements the new Hetzner DNS Cloud API" ([README](https://github.com/libdns/hetzner/blob/v2.0.1/README.md)). It is built on `hcloud-go/v2`, which pulls in the Prometheus client as a transitive dependency ([go.mod](https://github.com/libdns/hetzner/blob/v2.0.1/go.mod)). It implements Getter, Appender, Setter, Deleter and ZoneLister ([provider.go](https://github.com/libdns/hetzner/blob/v2.0.1/provider.go#L297-L304)). For ACME it calls the RRSet `add_records` and `remove_records` actions and waits for the resulting Hetzner Actions to finish ([provider.go L141](https://github.com/libdns/hetzner/blob/v2.0.1/provider.go#L141), [L261](https://github.com/libdns/hetzner/blob/v2.0.1/provider.go#L261)).
- **Empty token:** if `APIToken` is empty and `LIBDNS_HETZNER_TOKEN` is unset, the client constructor panics (`panic("hetzner: API token missing")`, [provider.go L54](https://github.com/libdns/hetzner/blob/v2.0.1/provider.go#L54)). caddy-dns/hetzner only checks for a missing token when parsing a Caddyfile, not in JSON `Provision` ([hetzner.go](https://github.com/caddy-dns/hetzner/blob/v2.0.1/hetzner.go)). certmagic's certificate job worker recovers panics and logs them ([certmagic async.go](https://github.com/caddyserver/certmagic/blob/v0.25.6/async.go)).
- **Registry and download-page trap (affects stock Caddy only):** Caddy's package registry API (`https://caddyserver.com/api/packages`, queried 2026-10-15) lists only `github.com/caddy-dns/hetzner`, the v1 path, last updated 2021. There is no `/v2` entry. The maintainer could not register `/v2` ("unable to scan modules in package github.com/caddy-dns/hetzner/v2", [caddyserver/caddy#7717](https://github.com/caddyserver/caddy/issues/7717)), and users report that the download page silently builds v1 ([caddy-dns/hetzner#29](https://github.com/caddy-dns/hetzner/issues/29), [#31](https://github.com/caddy-dns/hetzner/issues/31)). Two workarounds work: `xcaddy build --with github.com/caddy-dns/hetzner/v2`, or `caddy add-package github.com/caddy-dns/hetzner@v2.0.0` ([#29](https://github.com/caddy-dns/hetzner/issues/29), [README](https://github.com/caddy-dns/hetzner/blob/v2.0.1/README.md#building)). Embedding is unaffected because it uses ordinary Go module imports.
- **Other open items:**
  - [libdns/hetzner#14](https://github.com/libdns/hetzner/issues/14) (open): the challenge fails with "RRSet(s) is/are change protected". A contributor explains in [caddy-dns/hetzner#28](https://github.com/caddy-dns/hetzner/issues/28) that the failure only happens when the `_acme-challenge` TXT RRSet itself has Hetzner's change protection enabled.
  - Slow propagation: the README recommends `propagation_delay 30s` ([README](https://github.com/caddy-dns/hetzner/blob/v2.0.1/README.md), [#11](https://github.com/caddy-dns/hetzner/issues/11)).
  - Untested, from reading the source: the Caddyfile block form `hetzner { api_token … }` assigns `d.Val()` (the literal word `api_token`) before advancing to the argument, so it looks like it would fail with an argument error ([hetzner.go L46-L53](https://github.com/caddy-dns/hetzner/blob/v2.0.1/hetzner.go#L46-L53)). The positional form `dns hetzner <token>` and JSON config are not affected.
- **Hetzner API facts that matter for ACME:**
  - Hetzner DNS zones must be registrable names under a public suffix: "Subdomains are not supported" ([Cloud API reference: Zones](https://docs.hetzner.cloud/reference/cloud#tag/zones)).
  - `add_records` creates the RRSet if it is missing, otherwise appends to it. A `ttl` that differs from the existing RRSet's TTL is rejected ([OpenAPI spec](https://docs.hetzner.cloud/cloud.spec.json), `add_records`).
  - The rate limit is 3600 requests per hour per Project ([reference: Rate Limiting](https://docs.hetzner.cloud/reference/cloud#description/rate-limiting)).
  - Since 2026-09-30, `ttl` is required on the RRSet `change_ttl` action ([changelog](https://docs.hetzner.cloud/changelog)). libdns/hetzner v2.0.1 never calls `change_ttl`; `SetRecords` deletes and recreates the RRSet instead ([provider.go](https://github.com/libdns/hetzner/blob/v2.0.1/provider.go)). So this change does not affect it.

## 2. caddy-dns/cloudflare

- **Releases:** tags only, no GitHub Release objects. [v0.2.3](https://github.com/caddy-dns/cloudflare/tree/v0.2.3) (2026-02-11) and [v0.2.4](https://github.com/caddy-dns/cloudflare/tree/v0.2.4) (2026-03-23, "Handle new CF tokens format (#123)"). There has never been a v1, so the module path stays `github.com/caddy-dns/cloudflare`. [go.mod](https://github.com/caddy-dns/cloudflare/blob/v0.2.4/go.mod) requires `libdns/cloudflare v0.2.2` (2025-10-22) and `libdns/libdns v1.1.0`. It moved to the libdns 1.0 APIs in April 2025 ([commit bbf7911](https://github.com/caddy-dns/cloudflare/commit/bbf79111721a977fbd13dfe1f5b7aacaf7871a75)). The registry lists it under the same path, so the download page and `caddy add-package` work.
- **Config fields:** `api_token`, plus the deprecated `zone_token` ([cloudflare.go](https://github.com/caddy-dns/cloudflare/blob/v0.2.4/cloudflare.go), [README](https://github.com/caddy-dns/cloudflare/blob/v0.2.4/README.md)).
- **Token validation in `Provision`:** a token must match either `^[A-Za-z0-9_-]{35,50}$` (legacy) or `^cf(ut|at)_[A-Za-z0-9_-]{32,256}$` ([cloudflare.go L27-L30](https://github.com/caddy-dns/cloudflare/blob/v0.2.4/cloudflare.go#L27-L30)). v0.2.3 rejected Cloudflare's new scannable tokens, and users had to patch or fork the module ([#125](https://github.com/caddy-dns/cloudflare/issues/125)). Cloudflare says new tokens use the `cfut_` prefix (user tokens) or `cfat_` (account tokens), and existing tokens keep working ([token formats](https://developers.cloudflare.com/fundamentals/api/get-started/token-formats/), [create token](https://developers.cloudflare.com/fundamentals/api/get-started/create-token/)).
- **Rejected tokens are logged in full:** the validation error includes the entire token (`"API token '%s' appears invalid"`, [cloudflare.go L53](https://github.com/caddy-dns/cloudflare/blob/v0.2.4/cloudflare.go#L53)). The fix, [PR #132](https://github.com/caddy-dns/cloudflare/pull/132), is still open.
- **libdns/cloudflare:**
  - It looks up the DNS zone with `GET /zones?name=<zone>` and fails with `expected 1 zone, got N` unless exactly one zone comes back ([client.go L129-L146](https://github.com/libdns/cloudflare/blob/v0.2.2/client.go#L129-L146)). The caddy-dns README troubleshooting section links this error to local resolvers returning the wrong SOA and suggests `resolvers 1.1.1.1` ([README](https://github.com/caddy-dns/cloudflare/blob/v0.2.4/README.md#troubleshooting); [#133](https://github.com/caddy-dns/cloudflare/issues/133)).
  - The provider source carries `// TODO: Support retries and handle rate limits.` ([provider.go L18](https://github.com/libdns/cloudflare/blob/v0.2.2/provider.go#L18)).
  - `master` has an unreleased fix for long and non-ASCII TXT round-trips, dated 2026-06-27 ([c76b23d](https://github.com/libdns/cloudflare/commit/c76b23db635a6c4a2bea7eb8dfd62e3bccf71631)). ACME challenge values are short ASCII, so DNS-01 is not affected.
- **Other open issues (2026):** [#115](https://github.com/caddy-dns/cloudflare/issues/115) says the README's token wording no longer matches Cloudflare's user/account token terms. [#118](https://github.com/caddy-dns/cloudflare/issues/118) reports a failure with a host in a private subzone. [#117](https://github.com/caddy-dns/cloudflare/issues/117) is `caddy add-package` being given the module ID instead of the package path, which is user error.

## 3. libdns: the interface and what a new provider takes

**Core interfaces** ([libdns.go @ v1.1.1](https://github.com/libdns/libdns/blob/v1.1.1/libdns.go)):

```go
GetRecords(ctx, zone string) ([]Record, error)                 // RecordGetter
AppendRecords(ctx, zone string, recs []Record) ([]Record, error) // RecordAppender
SetRecords(ctx, zone string, recs []Record) ([]Record, error)    // RecordSetter
DeleteRecords(ctx, zone string, recs []Record) ([]Record, error) // RecordDeleter
ListZones(ctx) ([]Zone, error)                                   // ZoneLister (optional)
```

Contract points from the package docs:
- Record names are relative to the zone.
- Implementations must be safe for concurrent use and must honor context cancellation.
- Implementations should retry transient errors (5xx and 429) a few times without adding much latency.
- The caller is presumed to be the only thing changing its part of the zone ([README](https://github.com/libdns/libdns/blob/v1.1.1/README.md)).

libdns 1.0 shipped in April 2025, followed by v1.1.0 and v1.1.1 (2025-08-29). `v1.2.0-alpha.1` (2025-09-12) adds a `libdnstest` end-to-end test package that providers are invited to adopt ([release notes](https://github.com/libdns/libdns/releases/tag/v1.2.0-alpha.1)). Caddy 2.10 switched to the libdns 1.0 APIs, which forced every DNS module to update ([Caddy v2.10.0 notes](https://github.com/caddyserver/caddy/releases/tag/v2.10.0)).

**What each consumer needs:**
- **ACME DNS-01 (certmagic):** `certmagic.DNSProvider` is `libdns.RecordAppender` plus `libdns.RecordDeleter` ([solvers.go L589-L592](https://github.com/caddyserver/certmagic/blob/v0.25.6/solvers.go#L589-L592)). The solver must cope with several TXT records under the same name at once; issuing `example.com` and `*.example.com` together creates two `_acme-challenge.example.com` records ([solvers.go](https://github.com/caddyserver/certmagic/blob/v0.25.6/solvers.go)).
- **Caddy's per-issuer `challenges.dns.provider`:** the loaded module only needs to satisfy `certmagic.DNSProvider` ([acmeissuer.go L197-L216](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/acmeissuer.go#L197-L216)).
- **Caddy's global `apps.tls.dns` default (since 2.10):** the module must implement Appender, Deleter, Getter and Setter, or provisioning fails ([tls.go L179-L197](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/tls.go#L179-L197)).

**Adding a provider:**
1. **The libdns package.** Start from [libdns/template](https://github.com/libdns/template): a `Provider` struct whose config fields carry snake_case `json` tags, plus the four methods. The [wiki rules](https://github.com/libdns/libdns/wiki/Implementing-a-libdns-package) also require:
   - godoc on all exports;
   - minimal dependencies and no cgo;
   - configuration through struct fields, with environment variables only as defaults;
   - concurrency safety;
   - implicit provisioning (e.g. `sync.Once`) rather than an exported setup step;
   - a license compatible with MIT, Apache or BSD;
   - evidence of testing.

   To get into the `libdns` org you claim the provider in an issue, have the code reviewed in a PR, and then a repo is created for you to maintain.
2. **The Caddy wrapper,** only if the provider is configured through Caddy JSON. Start from [caddy-dns/template](https://github.com/caddy-dns/template). caddy-dns/hetzner shows the whole pattern in about 70 lines: embed the libdns `Provider`, return `ModuleInfo{ID: "dns.providers.<name>"}`, resolve placeholders in `Provision`, and optionally implement `UnmarshalCaddyfile` ([hetzner.go](https://github.com/caddy-dns/hetzner/blob/v2.0.1/hetzner.go)).
3. **Code that does not go through Caddy** (certmagic used directly, or a tnl-owned module) can use any libdns provider with no wrapper. The [libdns README](https://github.com/libdns/libdns/blob/v1.1.1/README.md) example shows it: configure the struct, call the methods.

**Ecosystem size** (GitHub API, 2026-10-15): 101 non-archived repos in the [libdns org](https://github.com/libdns) and 99 in the [caddy-dns org](https://github.com/caddy-dns). Providers are community-maintained by people who use them ([libdns README](https://github.com/libdns/libdns/blob/v1.1.1/README.md), [Caddy docs](https://caddyserver.com/docs/automatic-https)).

## 4. Minimum token scopes

### Cloudflare

| Call made by libdns/cloudflare | Endpoint | Accepted permission |
| --- | --- | --- |
| Find DNS zone ID by name | `GET /zones?name=` | "Zone Read" ([API ref](https://developers.cloudflare.com/api/resources/zones/methods/list/)) |
| Create TXT | `POST /zones/{id}/dns_records` | "DNS Write" ([API ref](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/create/)) |
| Look up record to delete | `GET /zones/{id}/dns_records` | "DNS Read" or "DNS Write" ([API ref](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/list/)) |
| Delete TXT | `DELETE /zones/{id}/dns_records/{rid}` | "DNS Write" ([API ref](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/delete/)) |

- **Minimum:** one API token with Zone Read and DNS Write, shown in the dashboard as *Zone · Zone · Read* and *Zone · DNS · Edit*, scoped "for the domain(s) you're managing" ([caddy-dns README](https://github.com/caddy-dns/cloudflare/blob/v0.2.4/README.md#configuration)).
- **The "Edit zone DNS" template is not enough.** Cloudflare's template grants only DNS Write ([templates](https://developers.cloudflare.com/fundamentals/api/reference/template/)), and the zone lookup also needs Zone Read.
- **The two READMEs disagree on scope.** libdns/cloudflare says a single token needs both permissions on "All zones" ([README](https://github.com/libdns/cloudflare/blob/v0.2.2/README.md#authenticating)). The caddy-dns README scopes it to the managed domain(s). From the endpoint permissions, a single token limited to one zone should be able to find that zone. This was not tested here.
- **The optional dual-token mode** separates a Zone Read `zone_token` from a DNS Write `api_token`. caddy-dns marks it deprecated and "might be removed" ([README](https://github.com/caddy-dns/cloudflare/blob/v0.2.4/README.md)).
- **Optional hardening:** a client IP filter (e.g. the Edge's IP) and a token TTL ([create token](https://developers.cloudflare.com/fundamentals/api/get-started/create-token/)).
- **Use API tokens, not the Global API Key** ([libdns README](https://github.com/libdns/cloudflare/blob/v0.2.2/README.md)).
- **User vs. account tokens:** user tokens (`cfut_`) act for a user. Account-owned tokens (`cfat_`) belong to the account, survive the user leaving, and support DNS and Zone permissions ([account-owned tokens](https://developers.cloudflare.com/fundamentals/api/get-started/account-owned-tokens/)).

### Hetzner

- **Minimum:** a Hetzner Cloud API token with **Read & Write** permission, created in the Project that contains the DNS zone. The token must be generated in the Hetzner Console under *Security → API Tokens*.
  - "A token is bound to a Project" ([Cloud API: Getting Started](https://docs.hetzner.cloud/reference/cloud#getting-started)).
  - The only permission levels are *Read*, which allows only GET, and *Read & Write*, which allows GET, POST, PUT and DELETE ([Generating an API token](https://docs.hetzner.com/cloud/api/getting-started/generating-api-token)).
- **No finer scope exists.** There is no per-zone, DNS-only or per-endpoint scoping. Hetzner: "As API tokens are scoped per project, you can restrict access to specific zones by placing them in separate projects" ([features and differences](https://docs.hetzner.com/networking/dns/migration-to-hetzner-console/features-and-differences)).
- **Consequence:** a token used for DNS-01 can also create or delete servers, volumes, firewalls and so on in the same Project.

## 5. Compiling DNS modules in

Caddy has no dynamic linking. A module gets "plugged in" by importing its package, whose `init()` registers it with the core ([architecture](https://caddyserver.com/docs/architecture), [extending Caddy](https://caddyserver.com/docs/extending-caddy)). Registration panics on a duplicate ID ("module already registered", [modules.go L157-L158](https://github.com/caddyserver/caddy/blob/v2.11.7/modules.go#L157-L158)). Both hetzner v1 and v2 use the ID `dns.providers.hetzner`, so only one of them can be linked.

- **Stock Caddy:** `xcaddy build --with github.com/caddy-dns/cloudflare --with github.com/caddy-dns/hetzner/v2` ([build docs](https://caddyserver.com/docs/build)). The alternatives are `caddy add-package` and the download page, which run through the registry; see the Hetzner v2 trap in §1. The `caddy` binary's own `main.go` describes the manual method: copy `main.go`, add imports, `go build` ([cmd/caddy/main.go](https://github.com/caddyserver/caddy/blob/v2.11.7/cmd/caddy/main.go)).
- **Embedded Caddy:** tnl's Go module would import `github.com/caddyserver/caddy/v2` (plus whichever standard modules it uses) and blank-import the DNS modules. xcaddy is not needed because xcaddy only generates such a module ([extending Caddy](https://caddyserver.com/docs/extending-caddy)). Go's minimum version selection picks the highest required versions. Today that means caddy-dns/hetzner v2.0.1 requires Caddy ≥ v2.11.2 and libdns v1.1.1, and caddy-dns/cloudflare v0.2.4 requires Caddy ≥ v2.7.5 and libdns v1.1.0, so the two are compatible ([hetzner go.mod](https://github.com/caddy-dns/hetzner/blob/v2.0.1/go.mod), [cloudflare go.mod](https://github.com/caddy-dns/cloudflare/blob/v0.2.4/go.mod)). This combination was not compiled here because no Go toolchain was available.
- **Caddy only accepts DNS providers as named modules, in both cases.** The ACME issuer's DNS solver is built from `challenges.dns.provider` (raw JSON, namespace `dns.providers`) or the TLS app's global `dns`, through `ctx.LoadModule`. The `solver` field is unexported ([acmeissuer.go L197-L216](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/acmeissuer.go#L197-L216), [automation.go L580-L608](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/automation.go#L580-L608)). So with Caddy's issuer, a provider must be a registered `dns.providers.*` module referenced by name in config; tnl cannot hand Caddy a live Go object.

## 6. Choosing the provider per Zone at runtime

Relevant Caddy JSON (`apps.tls.automation.policies[]`):
- `subjects` is "a filter, not a command". It selects which names a policy applies to; certificates are only obtained for names that are actually automated (site hostnames or the `automate` loader). "The first policy matching a certificate or subject name will be applied" (Caddy JSON docs API: `https://caddyserver.com/api/docs/config/apps/tls/automation/policies/`, rendered at [/docs/json/apps/tls/automation/policies/](https://caddyserver.com/docs/json/apps/tls/automation/policies/)).
- Matching uses `certmagic.MatchWildcard`. A policy with no subjects matches everything ([tls.go L906-L921](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/tls.go#L906-L921)).
- Each policy has its own `issuers`. An `acme` issuer has `challenges.dns` with these fields ([automation.go L580-L608](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/automation.go#L580-L608)), and the struct is still marked "experimental":
  - `provider`: module name plus that module's fields;
  - `ttl`, `propagation_delay`, `propagation_timeout` (default 2 min);
  - `resolvers`;
  - `override_domain`, for CNAME-delegated challenges.
- Since 2.10 there is also a global default, `apps.tls.dns`. Any issuer without a local provider falls back to it ([acmeissuer.go L208-L213](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/acmeissuer.go#L208-L213), [v2.10.0 notes](https://github.com/caddyserver/caddy/releases/tag/v2.10.0)).

The shape below follows from those fields: two Zones, each with its own provider and token, in one config.

```json
{"apps": {"tls": {"automation": {"policies": [
  {"subjects": ["*.a.example", "a.example"],
   "issuers": [{"module": "acme", "challenges": {"dns": {
     "provider": {"name": "hetzner", "api_token": "…"}, "propagation_delay": "30s"}}}]},
  {"subjects": ["*.b.example", "b.example"],
   "issuers": [{"module": "acme", "challenges": {"dns": {
     "provider": {"name": "cloudflare", "api_token": "…"}}}}]},
  {"issuers": [{"module": "acme"}]}
]}}}}
```

The last policy has no subjects, so it catches everything else (per-Hostname Zones) and uses the default HTTP-01/TLS-ALPN-01 challenges. Enabling DNS disables the other challenges for that issuer ([automatic HTTPS](https://caddyserver.com/docs/automatic-https)).

What counts as "runtime":
- **Choosing among compiled-in providers, and changing credentials:** yes. This is plain config, applied with `caddy.Load(json, …)` or the admin API.
- **How changes are applied:** `POST /load` replaces the config with "zero downtime" and rolls back on failure ([API docs](https://caddyserver.com/docs/api)). A path-level `POST /config/…` also re-encodes and reloads the entire config ([caddy.go L204-L247](https://github.com/caddyserver/caddy/blob/v2.11.7/caddy.go#L204-L247)), so editing one Zone re-provisions every module.
- **Using a provider that was not compiled in:** no. `LoadModule` fails for an unknown module name.
- **Where tokens end up:**
  - Pushed configs are autosaved to disk (mode 0600) unless `admin.config.persist` is `false` ([caddy.go L378-L391](https://github.com/caddyserver/caddy/blob/v2.11.7/caddy.go#L378-L391), [admin.go L126-L131](https://github.com/caddyserver/caddy/blob/v2.11.7/admin.go#L126-L131)).
  - The admin API returns the live config from `GET /config/` ([API docs](https://caddyserver.com/docs/api)).
  - Both caddy-dns modules expand `{env.*}` placeholders in `Provision` ([hetzner.go](https://github.com/caddy-dns/hetzner/blob/v2.0.1/hetzner.go), [cloudflare.go](https://github.com/caddy-dns/cloudflare/blob/v0.2.4/cloudflare.go)).

## 7. Cross-cutting facts that shape per-Zone wildcards

- **Wildcard reach:** a wildcard "can only match one label", so it covers single-label names under the domain "but not multiple levels" ([RFC 9525 §6.3, §7.1](https://www.rfc-editor.org/rfc/rfc9525.html)). Caddy qualifies a name for a wildcard only if its left-most label is `*` ([automatic HTTPS](https://caddyserver.com/docs/automatic-https)). A Zone's apex needs to be a separate name, either in the same certificate or in another one.
- **DNS-01 is required for wildcards at Let's Encrypt.** HTTP-01 and TLS-ALPN-01 cannot validate them. Let's Encrypt also warns that "Keeping API credentials on your web server is risky", and suggests narrow scopes or CNAME/NS delegation of `_acme-challenge` ([challenge types](https://letsencrypt.org/docs/challenge-types/)).
- **Wildcard preferred over per-name certs (Caddy ≥ 2.10):** Caddy skips individual certificates for subjects already covered by a wildcard it manages, unless the name is also listed in the `automate` loader. In the Caddyfile that is `force_automate` ([tls.go L605-L657](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/tls.go#L605-L657), [v2.10.0 notes](https://github.com/caddyserver/caddy/releases/tag/v2.10.0)).
- **Which DNS zone gets the TXT record:** certmagic resolves the DNS zone for `_acme-challenge.<name>` with an SOA lookup (`FindZoneByFQDN`) before calling the provider ([solvers.go L385](https://github.com/caddyserver/certmagic/blob/v0.25.6/solvers.go#L385)). A tnl Zone below the provider's DNS zone therefore works, but the token must cover the parent zone. The lookup goes through the Edge's resolvers, or `resolvers` if set, which is why split-horizon DNS breaks it ([caddy-dns/cloudflare README](https://github.com/caddy-dns/cloudflare/blob/v0.2.4/README.md#troubleshooting)). The solver does not follow CNAMEs; delegation goes through `override_domain` ([solvers.go L360-L362](https://github.com/caddyserver/certmagic/blob/v0.25.6/solvers.go#L339-L362)).

## Adjacent: DNS-PERSIST-01

Let's Encrypt announced DNS-PERSIST-01 on 2026-02-18. It is a persistent `_validation-persist.<domain>` TXT record bound to an ACME account, and `policy=wildcard` extends it to wildcards. Once that record is in place, renewals need no further DNS writes. The announcement planned staging for late Q1 2026 and production for Q2 2026 ([LE blog](https://letsencrypt.org/2026/02/18/dns-persist-01)). Boulder merged support for draft-00 and later draft-01 ([boulder#8660](https://github.com/letsencrypt/boulder/pull/8660), [#8725](https://github.com/letsencrypt/boulder/pull/8725)). Production availability as of 2026-10-15 was **not confirmed** from a primary source, and certmagic/acmez support was not checked. If it ships, it would be a provider-free way to get wildcards; it is out of scope for v1.
