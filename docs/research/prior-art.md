# Prior art: how existing tunnel tools split Edge and Connector

Research for [hyprsh/tnl#2](https://github.com/hyprsh/tnl/issues/2). It covers Pangolin (Gerbil, Newt and Traefik), cloudflared, frp, rathole, inlets and Tailscale Funnel. For each tool it covers enrolment and trust, where Routes live, the data path, reconnects and keepalives, and licence and maturity.

**Sources and snapshot.** Read on 2026-10-15. Source code was read at the commits linked below; documentation was fetched the same day. Every claim links to the file and line, or the doc page, that owns it. Star counts and release dates come from the GitHub API on the same day.

**Vocabulary.** When this note talks about tnl it uses the [glossary](../../GLOSSARY.md) terms: Edge, Connector, Tunnel, Route, Hostname, Zone and Service. When it describes another tool it uses that tool's own names and says which tnl role each one plays.

| Tool | Edge role | Connector role | Route-like object | Service-like object |
| --- | --- | --- | --- | --- |
| Pangolin | Pangolin server, Gerbil and Traefik on one host | Newt (the "site connector") | Resource and its targets | Target `host:port` |
| cloudflared | Cloudflare's network (SaaS) | `cloudflared` | Public hostname (DNS) plus an ingress rule | Ingress `service:` URL |
| frp | `frps` | `frpc` | `[[proxies]]` entry with `customDomains` | `localIP:localPort` |
| rathole | server | client | Service declared on both sides | `local_addr` |
| inlets | exit server | client | `--upstream host=url` plus `--letsencrypt-domain` | Upstream URL |
| Tailscale Funnel | Funnel relay plus Tailscale control plane (SaaS) | `tailscaled` or `tsnet` | Serve/Funnel config on the node | Proxy target |

Note that cloudflared's "tunnel" is a named object that several `cloudflared` processes (replicas) can run at once ([terms][cf-terms]). That is the meaning the glossary tells us to avoid for **Tunnel**.

---

## Answer

### Comparison

| | Enrolment and trust | Where Routes live and who changes them | Data path and transport | Root or TUN on the Connector? | Reconnect and keepalive | Licence and maturity |
| --- | --- | --- | --- | --- | --- | --- |
| **Pangolin** | An admin creates a site and gets an ID and a secret, stored hashed on the server. Alternatively a provisioning key (limited uses, expiry, optional "pending" approval) is exchanged once for an ID and secret. Newt trades the ID and secret for a session token and opens a websocket. It makes a **fresh WireGuard key on every start** and registers it over that websocket. | **Central**, in the Pangolin database. They are changed from the dashboard, the API or the CLI, or by a YAML blueprint. The **site itself can push** a blueprint, using its own site credentials. | Traefik on the server terminates TLS (Let's Encrypt HTTP-01), then proxies to `http://<site tunnel IP>:<internal port>` over Gerbil's **kernel WireGuard** interface (UDP 51820). Newt's userspace netstack accepts the connection and dials the target `host:port` on the LAN. | **No.** The default is wireguard-go with netstack. A TUN device is opt-in (`--native-main`). | Websocket ping every 30 s with a 60 s read deadline, and a fixed 3 s reconnect. WireGuard persistent keepalive is 5 s. An in-tunnel ping runs every 15 s and re-registers after 4 failures. The server marks a site offline after 2 min without a ping. | AGPL-3 plus a commercial Enterprise Edition (free under $100k revenue). Started 2024-09, about 23k stars, v1.24.0 (2026-09). Newt is being folded into the Pangolin CLI. |
| **cloudflared** | **Two credentials.** `cert.pem` is account-wide and can manage tunnels and DNS. The tunnel credential (JSON file or token) can **only run** that tunnel. Anyone holding the token can run the tunnel. | DNS CNAME `hostname → <UUID>.cfargotunnel.com` lives at Cloudflare. Ingress rules (hostname/path → service) live either in a local `config.yml` or **at Cloudflare, pushed down** with a version number. `cloudflared` matches the rules itself. | Cloudflare terminates TLS. Each request becomes its own **QUIC stream** (or HTTP/2 over TCP) to `cloudflared` on port 7844, with the request metadata attached. `cloudflared` matches Host and path and proxies to the service URL. | **No.** The docs recommend running it as a non-root user. | 4 connections to at least 2 data centres. QUIC keepalive every 1 s, idle timeout 5 s. Backoff of 1, 2, 4, 8, 16 s, then it **retries forever**. Falls back from QUIC to HTTP/2 when UDP fails. | Connector is Apache-2.0, started 2017, about 16k stars, 2026.10.0. **The Edge is proprietary and cannot be self-hosted.** |
| **frp** | **One shared token** for every `frpc` (sent as `md5(token+timestamp)`), or OIDC client credentials. An optional HTTP server plugin can veto a login or a new proxy. TLS is on by default, but **`frpc` does not verify the server certificate** unless a CA file is configured. | **The Connector declares them** (`frpc.toml`, the Store API, `frpc reload`). The first `frpc` to claim a domain wins. `frps` restricts what can be claimed through `allowPorts`, `subDomainHost` and plugins. | `frps` routes plain HTTP by Host, and routes HTTPS by **SNI passthrough** with no termination and no ACME. Each visitor connection takes a work connection: a yamux stream over TCP or TLS (KCP, QUIC or WebSocket also possible). `frpc` dials `localIP:localPort`. | **No.** | yamux keepalive every 30 s; the application heartbeat is off while mux is on. **Exits if the first login fails** (`loginFailExit` defaults to true). After that it backs off from 1 s up to 20 s and retries forever. | Apache-2.0, started 2015, about 110k stars, v0.71.1 (2026-10). Still 0.x, and the v2 rewrite has stalled. |
| **rathole** | A token per service, shared by both sides, checked by nonce challenge-response. **The default transport is plain TCP.** Noise NK (pins the server's static key) and TLS are optional. | **Declared on both sides.** The server owns `bind_addr` (the public port) and the client owns `local_addr`. The config file is hot-reloaded. There is **no Hostname routing**; the docs say to put nginx in front. | Visitor TCP or UDP reaches the server's port. A command goes down the control channel, and the client opens a data channel (one TCP connection per visitor connection) and dials `local_addr`. No multiplexing. | **No.** | The server sends a heartbeat every 30 s and the client times out after 40 s. Exponential backoff, capped at `retry_interval` (default 1 s), retries forever. | Apache-2.0 (Rust), started 2021-12, about 14k stars. **Last release v0.5.0 in 2023-10**, with occasional commits since. |
| **inlets** | **One token per exit server**, shared with every client. The control plane is `wss://…:8123`. With auto-TLS the server makes its own CA and the **client downloads it from that same server**. | Split. The **client declares** which hostname goes to which upstream (`--upstream`). The server lists the hostnames it will get certificates for (`--letsencrypt-domain`). | The exit server terminates TLS (Let's Encrypt HTTP-01) on 80/443. The request is carried over the TLS websocket to the client, which forwards it to the upstream URL. | **No** ("doesn't need … a tun device or NET_ADMIN"). | "The client will reconnect automatically." No intervals are published because the source is closed. | **Commercial EULA, closed-source binary**, and a licence check is needed to run it. inlets-pro started 2019, v0.12.0 (2026-10). The old open-source repository is gone. |
| **Tailscale Funnel** | The node joins the tailnet by SSO login or an auth key (one-off, reusable, ephemeral or pre-approved, at most 90 days). Node keys expire after 180 days unless expiry is disabled. An admin must grant the `funnel` node attribute in the tailnet policy. | **Declared on the node** (`tailscale funnel 3000`; `--bg` persists it). The admin decides *who* may use Funnel, not *what* they serve. The Hostname is fixed to `<node>.<tailnet>.ts.net`, and only ports 443, 8443 and 10000 are allowed. | Public DNS points at a Funnel relay. The relay opens a TCP proxy over the tailnet (WireGuard, direct or through DERP) to the node's `/v0/ingress` PeerAPI, passing source and target headers. **`tailscaled` terminates TLS** and proxies to the local target. The relay cannot decrypt the traffic. | `tailscaled` normally uses a TUN device. **`tsnet` and userspace-networking mode need no root**, and `tsnet` supports `ListenFunnel`. | The control-plane long-poll has keepalives and a 120 s watchdog. Peers ping the best path every 3 s while active. Falls back to the DERP relay when there is no direct path. | Client is BSD-3-Clause. **The coordination server and the Funnel relays are proprietary SaaS.** Headscale does not support Funnel. Started 2020, about 37k stars, v1.104.1 (2026-10). Bandwidth limits cannot be configured. |

### Implications for tnl

These are facts and trade-offs to feed into the grilling tickets. They are not decisions.

1. **Pangolin already ships the split tnl wants, and shows that the "no root on the Connector" constraint is achievable.** An authenticated control channel (a websocket) hands out WireGuard peer configuration and Route changes, and WireGuard carries the data. Newt runs wireguard-go on a userspace netstack with no TUN device. Gerbil uses a kernel interface on the server side, which matches "the Edge may use root". `tsnet` shows the same userspace approach for Tailscale. *(Data path #5)*

2. **Keep the credential that runs a Tunnel separate from the credential that changes Routes.** cloudflared splits these explicitly: `cert.pem` manages, while the tunnel JSON or token can only run. tnl's constraint that "Routes are managed from a CLI on the Connector" puts a Route-changing capability on the Connector's host. Pangolin is the cautionary example here: a blueprint pushed by a site is applied across the whole organisation and may refer to *other* sites by name. *(Joining #6, Route management #7)*

3. **Borrow single-use enrolment tokens; avoid shared global secrets.**
   - Pangolin's provisioning keys have a use limit and an expiry, can require approval, are swapped once for durable credentials, and are deleted from the config file afterwards.
   - Tailscale auth keys are one-off and expire.
   - frp and inlets authenticate every Connector with the same token, so revoking one Connector means rotating all of them.

   *(Joining #6)*

4. **Pin the Edge's identity instead of trusting whatever certificate the Edge presents.**
   - frp enables TLS but skips server-certificate verification by default.
   - inlets' auto-TLS has the client download the server's CA from the server it is connecting to.
   - rathole's Noise NK pins the server's key, and WireGuard's static public keys pin by design. A token that carries the Edge's public key (as cloudflared's token carries the account and tunnel IDs) is one way to give the Connector that pin.

   *(Joining #6)*

5. **Decide who turns a Hostname into a Service.**
   - **On the Edge (Pangolin).** The Edge stores the target `host:port`, gives each target an internal port on the Connector's tunnel IP, and tells Newt to listen on that port and forward. Newt itself is a plain layer-4 forwarder, and Traefik just dials `tunnelIP:port`.
   - **On the Connector (cloudflared, frp, inlets, Funnel).** The request crosses the tunnel with its Host or other metadata, and the Connector matches its own rules.

   Because tnl stores Routes on the Edge, either model works. cloudflared's "central store, pushed down with a monotonic version, Connector reports the last version it applied" is the closest fit for Connector-side matching. Pangolin's "per-target add/remove messages, plus a full sync on reconnect" fits Edge-side matching. *(Data path #5, Route management #7)*

6. **Central Route changes must reach the reverse proxy without a restart.**
   - Pangolin has Traefik poll an HTTP config provider every 5 s.
   - cloudflared swaps its ingress rules in place on each versioned push.
   - frp and rathole hot-reload their config files.

   tnl plans to put Caddy on the Edge, so the matching question is how Caddy reloads config, which is already listed under "Not yet specified".

7. **UDP-blocked networks are a known gap for designs that only use WireGuard.** cloudflared falls back from QUIC to HTTP/2 over TCP, and Tailscale relays over DERP. I found no TCP fallback for Pangolin's site tunnel, either in Newt's source or in Pangolin's NAT-traversal docs. A one-UDP-port tnl Edge has the same exposure unless a fallback is designed (this is already a "Not yet specified" item).

8. **Starting numbers for keepalive and backoff.**
   - Newt: control ping every 30 s with a read deadline of twice that, WireGuard keepalive 5 s, tunnel probe every 15 s with 4 failures before re-registering.
   - Pangolin server: marks a Connector offline after 2 min.
   - frp: yamux keepalive 30 s.
   - Everyone retries forever with capped backoff (cloudflared, frp, rathole).

   frp's default of exiting when the first login fails is the one to avoid. *(Not yet specified: reconnects and keepalive)*

9. **Where TLS ends is a trade-off; tnl has already chosen the Edge.**
   - TLS ends at the server in Pangolin, cloudflared and inlets HTTP mode, as it will in tnl. Certificates and ACME stay in one place, and the Edge sees plaintext.
   - Funnel is the opposite: TLS ends on the device and the relay cannot decrypt.
   - Funnel's ingress handshake (one stream per visitor connection, with the visitor's `ip:port` and the requested `host:port` in headers) is still a useful pattern for carrying that metadata across a Tunnel.

   *(Data path #5)*

10. **Avoid tying each Route to its own public port.** rathole, and frp and inlets in TCP mode, expose one port per Service. Routing by HTTP Host or SNI on 443 is what fits "TCP 80/443 plus one UDP port". Raw TCP is already listed as not yet specified.

---

## Pangolin (Pangolin + Gerbil + Newt + Traefik)

**Shape.** A self-hosted Pangolin server is several cooperating containers on one host:

- **Pangolin**, the control plane: database, dashboard, REST API and websocket.
- **Gerbil**, which manages WireGuard.
- **Traefik**, the ingress proxy, running in Gerbil's network namespace.
- **Badger**, a Traefik auth plugin.

Newt runs at each "site" ([architecture][pg-arch]; [installer compose][pg-compose]).

Pangolin's docs describe a public request as:

> Internet → Node ingress (Traefik) → Auth (Badger) → Tunnel (Gerbil) → Site connector (Newt) → Backend target

([architecture][pg-arch])

### Enrolment and trust

- **Credentials.** Every site is provisioned with an ID, a secret and an endpoint. The secret is checked against a hash stored on the server ([credentials][pg-creds]; [`verifyPassword` in getNewtToken.ts][pg-gettoken]).
- **Provisioning keys.** These are long-lived `spk-…` tokens with "a maximum usage count and an expiration time". A site swaps one, once, for its own ID and secret. The key is then removed from the JSON config file. Sites provisioned this way can optionally be held as **pending** until an admin approves them ([site provisioning][pg-prov]; [`provisionIfNeeded`][newt-prov]).
- **Session.** Newt POSTs the ID and secret to `/api/v1/auth/newt/get-token`. It then opens a websocket at `/api/v1/ws?token=…` ([client.go L409][newt-gettoken], [L584–592][newt-wsurl]).
- **WireGuard keys are throwaway.** Newt generates a new WireGuard private key every time it starts ([newt.go L108][newt-genkey]) and sends the public key in a `newt/wg/register` message. The server then tells Gerbil to add a peer with `allowedIps: [siteSubnet]` ([handleNewtRegisterMessage.ts L169][pg-addpeer]). The long-lived identity is the ID and secret; the WireGuard key is not.
- **Rotation.** Regenerating credentials is an Enterprise Edition feature. It offers "Regenerate" (the site stays connected until it restarts) and "Regenerate and Disconnect" ([credentials][pg-creds]).

### Routes

- **Storage and editing.** Resources and targets live in Pangolin's database. They can be edited from the dashboard, through `PUT /org/{orgId}/blueprint`, or with `pangolin apply blueprint` ([blueprints][pg-blueprints]).
- **Pushed from the site.** A site can push a blueprint itself, from `--blueprint-file` or from Docker labels, using its own ID and secret. Newt watches the file and resends it when it changes ([blueprint.go][newt-blueprint]). The docs call this "an ongoing source of truth". Targets with no `site` are assigned to the site that applied the blueprint, and the examples show such a blueprint also referring to *other* sites by name ([blueprints][pg-blueprints]). On the server, a site-applied blueprint is applied with the site's `orgId` ([handleApplyBlueprintMessage.ts L46–49][pg-applybp]).
- **How Traefik learns about Routes.** Traefik reads its routing from Pangolin's HTTP provider, polling every 5 s ([traefik_config.yml L5–8][pg-traefikcfg]).

### Data path

1. Traefik owns 80/443. It gets certificates through the `letsencrypt` resolver using the HTTP-01 challenge ([traefik_config.yml L26–33][pg-traefikcfg-le]).
2. For a Newt site, each target becomes a Traefik backend at `${method}://${siteTunnelIP}:${internalPort}` ([loadBalancer.ts L53–57][pg-lb]). The site's tunnel IP is the first address of the subnet assigned to that site ([handleNewtRegisterMessage.ts L231][pg-tunnelip]).
3. Gerbil creates a **kernel** WireGuard link through netlink (`LinkType: "wireguard"`) ([gerbil main.go L921][gerbil-link]). It needs `NET_ADMIN` and publishes UDP 51820, plus UDP 21820 for client hole-punching and relay ([compose L43–47][pg-compose]). Traefik shares Gerbil's network namespace (`network_mode: service:gerbil`), which is how it reaches tunnel IPs ([compose L56][pg-compose-netns]).
4. Newt brings up wireguard-go on a **userspace netstack** (`netstack.CreateNetTUN`) by default. It only creates a real TUN device with `--native-main` ([connect.go L83–112][newt-netstack]; [flag][newt-nativeflag]). Targets arrive as `listenPort:host:targetPort` ([targets.go][newt-targets]; `newt/tcp/add` handler [handlers.go L185][newt-tcpadd]). Newt listens on `listenPort` inside the netstack and dials `host:targetPort`, which can be any address it can reach, not just localhost.
5. Newt's README sums it up as "a fully user space WireGuard tunnel client and TCP/UDP proxy" ([README][newt-readme]).

### Reconnect and keepalive

- **Websocket.** Ping every 30 s ([newt.go L131–136][newt-newclient]), with a read deadline of twice the ping interval (minimum 20 s) ([client.go L156–161][newt-pongwait]). A failed connection is retried on a fixed 3 s interval ([client.go L169][newt-reconnect]).
- **WireGuard.** `persistent_keepalive_interval=5` ([connect.go L149][newt-wgka]).
- **In-tunnel health check.** A ping every 15 s with a 7 s timeout (defaults in [newtconfig.go L341–342][newt-pingdefaults]). After 4 consecutive failures Newt asks the server for exit nodes and re-registers. While failing it stretches the interval up to 6 s ([ping.go L222–300][newt-pingcheck]).
- **Server side.** A site is marked offline after 2 min with no ping and no live websocket ([offlineChecker.ts L9–10][pg-offline]).
- **Docs.** Pangolin's NAT-traversal docs describe relaying through the server when hole-punching fails. I found no TCP fallback for the site's WireGuard tunnel ([NAT traversal][pg-nat]).

### Licence and maturity

- **Licences.** Pangolin's repository defaults to AGPL-3. Files carrying the "Fossorial Commercial License" header are commercial ([LICENSE][pg-license]). Gerbil and Newt are AGPL-3 with a commercial option ([gerbil README][gerbil-readme]; [newt README][newt-readme]). The Enterprise Edition is "free for personal use and organizations with less than $100,000 USD gross annual revenue", and still needs a licence key ([enterprise edition][pg-ee]).
- **Maturity.** Started 2024-09. About 23k stars. Latest release 1.24.0 on 2026-09-30 (GitHub API).
- **Newt's future.** Newt "is being phased out in favor of the Pangolin CLI. Use `pangolin site up`" ([newt README L7][newt-readme]). The CLI (`fosrl/cli`) imports `github.com/fosrl/newt` as a library (its `go.mod`).

## cloudflared (Cloudflare Tunnel)

**Shape.** `cloudflared` makes outbound-only connections to Cloudflare's network, and traffic then flows both ways over them ([overview][cf-overview]). The Edge is Cloudflare's own global network, so it cannot be self-hosted.

### Enrolment and trust

- **Locally-managed tunnels use two files** ([local tunnel permissions][cf-localperms]):
  - `cert.pem` comes from `cloudflared tunnel login`. It is account-wide, valid for at least 10 years, and "allows users to create, delete, and manage all tunnels for the account".
  - `<UUID>.json` comes from `cloudflared tunnel create`. It "only allows the user to run that specific tunnel, and do nothing else", and does not expire.
- **Remotely-managed tunnels use a token.** "Anyone with access to the token will be able to run the tunnel" ([remote tunnel permissions][cf-remoteperms]). The token is base64 JSON containing the account tag, tunnel secret, tunnel ID and an optional endpoint ([connection.go L65–94][cf-creds]).
- **No origin listener.** Origin traffic is "already authenticated using your tunnel's connector credentials", so Authenticated Origin Pulls doesn't apply ([overview][cf-overview]).

### Routes

- **Hostname to tunnel.** This is a DNS CNAME from the hostname to `<UUID>.cfargotunnel.com`, which only proxies for DNS records in the same account ([DNS records][cf-dns]). `cloudflared tunnel route dns` creates it from the command line, but needs `cert.pem` ([DNS records][cf-dns]).
- **Hostname to service.** These are ingress rules, matched top to bottom on hostname and path, with a required catch-all rule at the end ([configuration file][cf-config]).
- **Where the rules live.** For locally-managed tunnels they live in `config.yml` on the Connector's host. For remotely-managed tunnels, "tunnel configuration is stored in Cloudflare" and is managed from the dashboard or the API ([terms][cf-terms]).
- **How changes arrive.** The Edge pushes them with a version number. `cloudflared` ignores any version that isn't newer than the one it has, swaps the ingress rules in place, and reports `LastAppliedVersion` back ([orchestrator.go L104–129][cf-orch]).

### Data path

1. Cloudflare terminates the visitor's TLS.
2. `cloudflared` connects to the Edge on port 7844: UDP for `quic`, TCP for `http2` ([firewall][cf-firewall]).
3. The default `auto` protocol "starts with QUIC and falls back to HTTP/2" ([protocol.go L10][cf-protocol]; [run parameters][cf-runparams]).
4. Every proxied request arrives as its own stream carrying a `ConnectRequest`, whose type is HTTP, WebSocket or TCP ([quic_connection.go L234–252][cf-dispatch]).
5. `cloudflared` picks the ingress rule with `FindMatchingRule(req.Host, req.URL.Path)` ([proxy.go L135][cf-match]) and proxies to that rule's service URL.
6. The docs advise running `cloudflared` "as a non-root user with minimal permissions" ([configure][cf-configure]). No TUN device is involved in publishing hostnames.

### Reconnect and keepalive

- **Redundant connections.** Four outbound connections "to four different servers spread across at least two distinct data centers" ([availability][cf-avail]; `ha-connections` default 4 in [cmd.go L790–793][cf-cmdflags]).
- **QUIC timers.** Keepalive every 1 s, idle timeout 5 s, handshake idle timeout 5 s ([quic/constants.go][cf-quicconst]; applied in [supervisor/tunnel.go L587–590][cf-quiccfg]).
- **Backoff.** `--retries` defaults to 5, with exponential backoff "retrying at 1, 2, 4, 8, 16 seconds" ([run parameters][cf-runparams]). The supervisor builds its backoff with `retryForever=true`, so it never gives up ([supervisor.go L130][cf-supervisor]; [backoffhandler.go][cf-backoff]).
- **Protocol fallback.** After the retries are used up, it falls back to the other protocol ([supervisor/tunnel.go][cf-fallback]).
- **Shutdown.** On SIGTERM it stops taking new requests and drains for `--grace-period` (default 30 s) ([run parameters][cf-runparams]).

### Licence and maturity

- **Licence.** `cloudflared` is Apache-2.0 ([LICENSE][cf-license]).
- **Maturity.** Started 2017. About 16k stars. Latest release 2026.10.0 (GitHub API).
- **Not self-hostable.** The Edge side (Cloudflare's network and API) is a proprietary service.

## frp

**Shape.** `frps` runs on the public server and `frpc` runs on the Connector's host. Both are configured with TOML.

### Enrolment and trust

- **Auth methods.** `auth.method` is either `token` (the default) or `oidc` ([README L639–700][frp-readme-auth]).
- **Token auth.** Every `frpc` uses the same `auth.token`. It is sent as `md5(token + timestamp)` ([util.go L50–56][frp-authkey]; [token.go][frp-token]).
- **OIDC.** Uses the client-credentials grant ([README][frp-readme-auth]).
- **Server plugins.** These receive HTTP RPCs for operations such as `Login` and `NewProxy`, and can reject them or rewrite their content ([server_plugin.md][frp-serverplugin]; [frps example L159–169][frp-frps-plugins]).
- **TLS is on but unverified by default.** TLS has been the default "since v0.50.0" ([README L712][frp-readme-tls]). However, the client TLS config sets `InsecureSkipVerify = true` unless a trusted CA file is given ([tls.go L127–153][frp-tlsverify]). By default, then, `frpc` does not verify which server it is talking to.

### Routes

- **Declared by the Connector.** For example, a `[[proxies]]` entry with `type = "http"`, `customDomains` or `subdomain`, and `localIP`/`localPort` ([frpc example L236–260][frp-frpc-example]).
- **Changing them.** `frpc reload` hot-reloads proxies ([README L808–820][frp-readme-reload]). An optional "Store" lets proxies be created, updated and deleted at runtime through `frpc`'s web UI or API, and persists them to disk ([README L614–627][frp-readme-store]).
- **Server-side limits.** `allowPorts` and `subDomainHost` ([frps example][frp-frps-example]), plus server plugins.
- **Conflicts.** If two `frpc` processes claim the same route, the second gets `router config conflict` ([router.go L11][frp-conflict]).

### Data path

1. **HTTP.** `frps` listens on `vhostHTTPPort` and routes plain HTTP by `Host` through a reverse proxy ([frps example L52–53][frp-frps-example]; [vhost/http.go][frp-vhosthttp]).
2. **HTTPS.** On `vhostHTTPSPort`, `frps` only reads the ClientHello SNI and passes the connection through ([https.go L39][frp-sni]). The repository has no ACME support. To terminate TLS you either put certificates on `frpc` (the `https2http` plugin, [README L387–410][frp-readme-https]) or put another proxy in front of `frps`.
3. **Per visitor connection.** `frps` takes a pooled work connection, or asks `frpc` for one with `ReqWorkConn` ([control.go L595–615][frp-workconn]).
4. **Multiplexing.** Work connections are multiplexed over the single control connection by yamux, which is on by default ([README L877–886][frp-readme-mux]).
5. **Transports.** TCP (default), KCP, QUIC, WebSocket or WSS ([client.go L102–106][frp-transport]).
6. **Privileges.** Everything is ordinary sockets; no root and no TUN device.

### Reconnect and keepalive

- **Keepalive.** With `tcpMux` on, the yamux keepalive is 30 s and the application heartbeat is turned off (`-1`). With mux off, the heartbeat is 30 s with a 90 s timeout ([client.go L148–166][frp-transport]).
- **First login.** `loginFailExit` defaults to `true` ([client.go L54–57, L88][frp-loginfail]), so if the first login fails `frpc` exits ([service.go L276–280][frp-service]).
- **After a successful login.** It reconnects forever with a "fast" backoff: three quick retries, then exponential from 1 s up to 20 s ([service.go L300–329][frp-service-backoff]).

### Licence and maturity

- **Licence.** Apache-2.0 ([LICENSE][frp-license]).
- **Maturity.** Started 2015. About 110k stars. Latest release v0.71.1 (2026-10-14), still 0.x.
- **v2.** The README says a v2 rewrite is "much higher [complexity] than anticipated", and that the current version will keep being iterated on instead ([README L141–145][frp-readme-v2]).

## rathole

**Shape.** One Rust binary, run in server or client mode. It is "similar to frp", but "configuration of a service is split into the client side and the server side, and a token is mandatory" ([README L42][rh-readme]).

### Enrolment and trust

- **Tokens.** Each service has its own token, which must be the same on both sides ([README L35][rh-readme-sec]).
- **Handshake.** When a control channel opens, the server sends a nonce, and the client must answer as the service it claims to be ([internals][rh-internals]). The answer is a SHA-256 over the token and the nonce ([server.rs L287–321][rh-nonce]; [protocol.rs L20–26][rh-proto]).
- **Transport encryption.** The default transport is **plain TCP** (`type = "tcp"`). `tls` and `noise` are options ([README L114][rh-readme-transport]).
- **Noise.** The default Noise pattern is `Noise_NK_25519_ChaChaPoly_BLAKE2s`, which authenticates the server "just like TLS with properly configured certificates". `XX` and `KK` patterns are also offered ([transport.md L58][rh-noise]).

### Routes

- **Both sides declare each service.** Names must match: the server sets `bind_addr` (the public port) and the client sets `local_addr` ([README config spec][rh-readme-config]).
- **Hot reload.** Editing the config file adds or removes services without a restart ([README L36][rh-readme-sec]; [config_watcher.rs][rh-watcher]).
- **No Hostname routing, by design.** "Domain based forwarding for HTTP" is out of scope; the docs say to "use nginx to do this and set `rathole` as the upstream" ([out-of-scope.md][rh-oos]).

### Data path

- **Control channels.** The client opens one control channel per service. When a visitor connects to the server's `bind_addr`, the server sends a command down that control channel.
- **Data channels.** The client then opens a data channel (a new TCP connection) for that visitor and dials `local_addr`. "The server also creates a few data channels in advance" ([internals][rh-internals]).
- **No multiplexing.** Every visitor connection gets its own TCP connection back to the server.
- **Protocols.** TCP and UDP services. No root and no TUN device.

### Reconnect and keepalive

- **Heartbeat.** The server sends one every 30 s, and the client times out after 40 s ([README L110, L147][rh-readme-config]).
- **Reconnect.** The control channel reconnects with exponential backoff (multiplier 3, jitter 0.2), capped at `retry_interval` (default 1 s), with no overall time limit ([constants.rs L18–25][rh-backoff]; [README L111][rh-readme-config]).

### Licence and maturity

- **Licence.** Apache-2.0 ([LICENSE][rh-license]).
- **Maturity.** Started 2021-12. About 14k stars.
- **Activity.** The latest release is v0.5.0 from 2023-10-01. Since then there have been a handful of commits, the most recent on 2026-08-23 (GitHub API).

## inlets

**Shape.** One binary (`inlets-pro`) runs as either server or client, "entirely within userspace" ([README][in-readme]).

- **Two planes.** The *control plane* is a TLS websocket on port 8123. The *data plane* is the port or ports that visitors use ([CLI reference L43–56][in-cli-topology]; [FAQ L258–262][in-faq-planes]).
- **Modes.** HTTP mode adds "TLS termination on the exit-server" ([CLI reference L316–319][in-cli-http]).

### Enrolment and trust

- **Token.** The client authenticates with "an API token which is generated by the tunnel administrator" ([FAQ L286–288][in-faq-sec]). The manual walkthrough uses one token per server, which every client uses ([manual HTTP server][in-manual]).
- **Auto-TLS.** "Auto-TLS will create a Certificate Authority CA and start serving it via the control-plane port" ([CLI reference L202][in-cli-autotls]). You must turn auto-TLS off "so that the client does not attempt to download the server's generated CA" ([CLI reference L267][in-cli-noautotls]). In other words, by default the client fetches its trust anchor from the server it is connecting to.

### Routes

- **The server picks the hostnames it will get certificates for**, with repeated `--letsencrypt-domain` flags ([manual HTTP server L40][in-manual]).
- **The client picks where each hostname goes**, with `--upstream prometheus.example.com=http://127.0.0.1:9090,grafana.example.com=http://127.0.0.1:3000` ([CLI reference L375][in-cli-upstream]). The upstream can be another LAN host ([CLI reference L79–102][in-cli-upstreamhost]).
- **Several clients.** More than one client can connect to the same server, either with different hostnames or with the same hostname "for high availability" ([manual HTTP server L99–100][in-manual]). The server load-balances between them ([CLI reference L55][in-cli-ha]).

### Data path

1. The exit server takes ports 80 and 443 and gets Let's Encrypt certificates with HTTP-01 ([CLI reference L331–360][in-cli-le]).
2. Requests travel over the websocket, which is TCP "so that it can penetrate HTTP proxies, captive portals, firewalls" ([FAQ L147–151][in-faq-tcp]).
3. The client forwards each request to its upstream.
4. It "doesn't need to run as root, doesn't depend on iptables, doesn't need a tun device or NET_ADMIN capability" ([FAQ L12][in-faq-root]).

### Reconnect and keepalive

"The client will reconnect automatically", and the docs suggest running it under systemd ([FAQ L330][in-faq-reconnect]). No intervals are published, and the source is closed.

### Licence and maturity

- **Commercial.** "inlets Pro is licensed as commercial software and must not be used without a valid license key from OpenFaaS Ltd." ([EULA][in-eula]; [README L44][in-readme-license]).
- **Licence server.** Subscription licences must be able to reach the Gumroad licence server ([FAQ L346][in-faq-license]).
- **Repository.** The `inlets/inlets-pro` repository holds docs, a Helm chart and release artefacts, but no source code ([repo][in-repo]).
- **The old open-source repository is gone.** `github.com/inlets/inlets` returns 404 from the GitHub API, and it is not in the `inlets` organisation's repository list (checked 2026-10-15).
- **Maturity.** inlets-pro was created in 2019. Latest release 0.12.0 on 2026-10-08 (GitHub API).

## Tailscale Funnel

**Shape.** The Edge role is split between Tailscale's **coordination server** (identity, policy and peer distribution) and **Funnel relay servers** (public ingress). Both are run by Tailscale. The Connector role is played by `tailscaled`, or by a program that embeds `tsnet`.

### Enrolment and trust

- **Joining the tailnet.** A node joins by interactive login or with an auth key. Auth keys come in one-off, reusable, ephemeral, pre-approved and tagged forms, and expire after at most 90 days ([auth keys][ts-authkeys]).
- **Node keys.** These expire after 180 days by default. Expiry can be disabled per device, and devices that are tagged when first authenticated have it disabled ([key expiry][ts-keyexpiry]).
- **Funnel requirements** ([Funnel][ts-funnel]):
  - MagicDNS and HTTPS certificates must be enabled.
  - The tailnet policy must give the device a `funnel` node attribute ("`nodeAttrs`").
  - Only an Owner, Admin or Network admin can change that policy.
- **On the node.** It refuses ingress from peers that lack the ingress capability ([serve.go L1505–1510][ts-ingress]).

### Routes

- **Configured on the device.** For example `tailscale funnel 3000`. With `--bg`, the config "resumes automatically after a reboot" ([funnel CLI][ts-funnelcli]).
- **Fixed Hostname.** It is always the node's name under `tailnet-name.ts.net`, with no custom domains, on ports 443, 8443 or 10000 only ([Funnel][ts-funnel]).
- **Proxy targets.** The docs say "only `http://127.0.0.1` is currently supported for proxies" ([funnel CLI][ts-funnelcli]). The shared target parser, however, accepts non-loopback hosts as long as a scheme is given ([ipn/serve.go L763–830][ts-expand]).
- **Who controls what.** The administrator decides *who* may use Funnel. The device decides *what* it serves.

### Data path

1. Public DNS for the Funnel URL returns a Funnel relay's IP address. "The Funnel relay server creates a TCP proxy to your device over Tailscale" and "cannot decrypt data sent over this proxy" ([Funnel][ts-funnel]).
2. In code, the relay POSTs to the node's PeerAPI at `/v0/ingress` with `Tailscale-Ingress-Src` (the visitor's `ip:port`) and `Tailscale-Ingress-Target` (`host:port`). The node takes over ("hijacks") that HTTP connection and uses it as a raw two-way stream ([serve.go L1497–1560][ts-ingress]).
3. The node rejects targets that have no Funnel configured ([serve.go L461–479][ts-handleingress]).
4. `tailscaled` "terminates the TLS connection and passes the decrypted request to the local service" ([Funnel][ts-funnel]).
5. **Privileges.** `tsnet` runs "a fully self-contained Tailscale node inside your process using a userspace TCP/IP stack (gVisor)", with "No root privileges required" ([tsnet.go L1–25][ts-tsnet]). It exposes `ListenFunnel` ([tsnet.go L1498–1513][ts-listenfunnel]). `tailscaled` itself also has a userspace-networking mode for hosts that cannot create a TUN device ([userspace networking][ts-userspace]).

### Reconnect and keepalive

- **Control plane.** The client ends its long-poll to the coordination server if it hears nothing for 120 s; it expects "a keep alive ping every minute" ([direct.go L1015–1018][ts-watchdog]).
- **Data plane.** The best UDP path is pinged every 3 s while a session is active, and peerings are kept up for 45 s after the last activity ([magicsock.go L4068–4084][ts-magicsock]).
- **Relays.** When no direct path is possible, traffic falls back to peer relays and then to DERP relay servers, which "blindly forward already-encrypted traffic" ([DERP][ts-derp]).

### Licence and maturity

- **Licence.** The client is BSD-3-Clause ([LICENSE][ts-license]).
- **Closed parts.** The coordination server is closed source ([open source][ts-oss]).
- **Bandwidth.** "Traffic sent over a Funnel is subject to non-configurable bandwidth limits" ([Funnel][ts-funnel]).
- **Self-hosting.** Headscale, the community-run self-hosted coordination server, lists Funnel and Serve as unsupported ([Headscale features][hs-features]). In practice Funnel only works with Tailscale's SaaS.
- **Maturity.** Started 2020. About 37k stars. Latest release v1.104.1 (2026-10-07) (GitHub API).

---

## Cross-cutting observations

- **Control channel and data channel.**
  - Pangolin: websocket for control, WireGuard for data.
  - cloudflared: an RPC stream inside the same QUIC or HTTP/2 connection.
  - frp: a control message stream and work connections, both multiplexed over one yamux connection.
  - rathole: a control channel per service, plus one TCP data channel per visitor.
  - inlets: both run over one websocket.
  - Tailscale: an HTTP long-poll to the coordination server for control, WireGuard (or DERP) for data.

  Pangolin and Tailscale are the two that put the data on WireGuard, as tnl plans to.

- **Two families of data path.**
  - **Layer-4 forwarding.** Pangolin/Newt, rathole, and frp and inlets in TCP mode. The address on the Edge side (a tunnel IP and port, or a public port) already says which Service is meant.
  - **Per-request or per-connection metadata.** cloudflared's `ConnectRequest`, frp's Host and SNI routing, inlets HTTP mode, and Funnel's ingress headers. The Connector reads the metadata to pick the Service.

- **Who writes Routes.**
  - **Central** (Pangolin, remotely-managed cloudflared).
  - **The Connector declares them and the Edge accepts** (frp, inlets, Funnel within the limits of its policy).
  - **Both sides must agree** (rathole).

  Only Pangolin, with site blueprints, combines "stored centrally" with "changed from the Connector", which is tnl's constraint.

- **Where TLS terminates.**
  - At the Edge: Pangolin, cloudflared, inlets HTTP mode.
  - At the Connector: Funnel, and frp with the `https2http` plugin.
  - Not handled at all: rathole.

- **Services on the LAN.** Every tool except Funnel (according to its docs) lets the Connector reach Services on other LAN hosts, not just localhost.

## Questions this surfaced (for downstream tickets)

- **Scope of the Connector's management credential.** When the `tnl` CLI on a Connector adds a Route, can it point that Route at *another* Connector, or claim a Hostname in any Zone? Pangolin's site blueprints allow cross-site references. cloudflared avoids the question by keeping management (`cert.pem`) separate from running. *(Joining #6, Route management #7)*
- **Does the Edge or the Connector turn a Hostname into a Service?** This decides whether the Tunnel carries per-request metadata (cloudflared or Funnel style) or the Edge dials a per-Route port inside the Connector's netstack (Pangolin style). *(Data path #5)*
- **Do we need a TCP fallback for networks that block UDP?** Pangolin has none for sites. cloudflared and Tailscale both ship one. This is already listed under "Not yet specified" on the map; prior art suggests mature tools treat it as necessary.

---

<!-- Source permalinks -->

[pg-arch]: https://docs.pangolin.net/development/system-architecture
[pg-creds]: https://docs.pangolin.net/manage/sites/credentials
[pg-prov]: https://docs.pangolin.net/manage/sites/site-provisioning
[pg-blueprints]: https://docs.pangolin.net/manage/blueprints
[pg-nat]: https://docs.pangolin.net/manage/clients/nat-traversal
[pg-ee]: https://docs.pangolin.net/self-host/enterprise-edition
[pg-gettoken]: https://github.com/fosrl/pangolin/blob/2a4004044d8291fd66b1ac2202c49628de0a95ff/server/routers/newt/getNewtToken.ts#L22-L92
[pg-addpeer]: https://github.com/fosrl/pangolin/blob/2a4004044d8291fd66b1ac2202c49628de0a95ff/server/routers/newt/handleNewtRegisterMessage.ts#L169-L172
[pg-tunnelip]: https://github.com/fosrl/pangolin/blob/2a4004044d8291fd66b1ac2202c49628de0a95ff/server/routers/newt/handleNewtRegisterMessage.ts#L224-L235
[pg-applybp]: https://github.com/fosrl/pangolin/blob/2a4004044d8291fd66b1ac2202c49628de0a95ff/server/routers/newt/handleApplyBlueprintMessage.ts#L46-L49
[pg-lb]: https://github.com/fosrl/pangolin/blob/2a4004044d8291fd66b1ac2202c49628de0a95ff/server/lib/traefik/loadBalancer.ts#L45-L58
[pg-traefikcfg]: https://github.com/fosrl/pangolin/blob/2a4004044d8291fd66b1ac2202c49628de0a95ff/install/config/traefik/traefik_config.yml#L5-L8
[pg-traefikcfg-le]: https://github.com/fosrl/pangolin/blob/2a4004044d8291fd66b1ac2202c49628de0a95ff/install/config/traefik/traefik_config.yml#L26-L33
[pg-compose]: https://github.com/fosrl/pangolin/blob/2a4004044d8291fd66b1ac2202c49628de0a95ff/install/config/docker-compose.yml#L29-L52
[pg-compose-netns]: https://github.com/fosrl/pangolin/blob/2a4004044d8291fd66b1ac2202c49628de0a95ff/install/config/docker-compose.yml#L53-L58
[pg-offline]: https://github.com/fosrl/pangolin/blob/2a4004044d8291fd66b1ac2202c49628de0a95ff/server/routers/newt/offlineChecker.ts#L9-L10
[pg-license]: https://github.com/fosrl/pangolin/blob/2a4004044d8291fd66b1ac2202c49628de0a95ff/LICENSE#L1-L30
[gerbil-readme]: https://github.com/fosrl/gerbil/blob/3d7c34742f18ee039f3539d4a01daf55993ee638/README.md?plain=1
[gerbil-link]: https://github.com/fosrl/gerbil/blob/3d7c34742f18ee039f3539d4a01daf55993ee638/main.go#L921-L927
[newt-readme]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/README.md?plain=1#L7-L11
[newt-prov]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/websocket/config.go#L142-L200
[newt-gettoken]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/websocket/client.go#L353-L410
[newt-wsurl]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/websocket/client.go#L584-L592
[newt-pongwait]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/websocket/client.go#L156-L161
[newt-reconnect]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/websocket/client.go#L169
[newt-genkey]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/newt/newt.go#L108-L112
[newt-newclient]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/newt/newt.go#L131-L139
[newt-netstack]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/newt/connect.go#L83-L112
[newt-wgka]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/newt/connect.go#L144-L149
[newt-nativeflag]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/newtconfig/newtconfig.go#L535
[newt-pingdefaults]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/newtconfig/newtconfig.go#L341-L342
[newt-pingcheck]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/newt/ping.go#L222-L300
[newt-blueprint]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/newt/blueprint.go#L32-L127
[newt-targets]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/newt/targets.go#L29-L60
[newt-tcpadd]: https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/newt/handlers.go#L185
[cf-overview]: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/
[cf-terms]: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/get-started/tunnel-useful-terms/
[cf-localperms]: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/tunnel-permissions/
[cf-remoteperms]: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/remote-tunnel-permissions/
[cf-dns]: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/routing-to-tunnel/dns/
[cf-config]: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/configuration-file/
[cf-configure]: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/#secure-your-tunnel
[cf-avail]: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-availability/
[cf-firewall]: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-with-firewall/
[cf-runparams]: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/run-parameters/
[cf-creds]: https://github.com/cloudflare/cloudflared/blob/cfb7560389a6071648370fca3182c9f67d934b92/connection/connection.go#L65-L94
[cf-protocol]: https://github.com/cloudflare/cloudflared/blob/cfb7560389a6071648370fca3182c9f67d934b92/connection/protocol.go#L10-L18
[cf-dispatch]: https://github.com/cloudflare/cloudflared/blob/cfb7560389a6071648370fca3182c9f67d934b92/connection/quic_connection.go#L234-L260
[cf-match]: https://github.com/cloudflare/cloudflared/blob/cfb7560389a6071648370fca3182c9f67d934b92/proxy/proxy.go#L135
[cf-orch]: https://github.com/cloudflare/cloudflared/blob/cfb7560389a6071648370fca3182c9f67d934b92/orchestration/orchestrator.go#L104-L135
[cf-quicconst]: https://github.com/cloudflare/cloudflared/blob/cfb7560389a6071648370fca3182c9f67d934b92/quic/constants.go#L5-L12
[cf-quiccfg]: https://github.com/cloudflare/cloudflared/blob/cfb7560389a6071648370fca3182c9f67d934b92/supervisor/tunnel.go#L587-L592
[cf-cmdflags]: https://github.com/cloudflare/cloudflared/blob/cfb7560389a6071648370fca3182c9f67d934b92/cmd/cloudflared/tunnel/cmd.go#L782-L793
[cf-supervisor]: https://github.com/cloudflare/cloudflared/blob/cfb7560389a6071648370fca3182c9f67d934b92/supervisor/supervisor.go#L130
[cf-backoff]: https://github.com/cloudflare/cloudflared/blob/cfb7560389a6071648370fca3182c9f67d934b92/retry/backoffhandler.go#L25-L45
[cf-fallback]: https://github.com/cloudflare/cloudflared/blob/cfb7560389a6071648370fca3182c9f67d934b92/supervisor/tunnel.go#L330-L370
[cf-license]: https://github.com/cloudflare/cloudflared/blob/cfb7560389a6071648370fca3182c9f67d934b92/LICENSE
[frp-readme-auth]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/README.md?plain=1#L639-L700
[frp-readme-tls]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/README.md?plain=1#L710-L732
[frp-readme-https]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/README.md?plain=1#L387-L410
[frp-readme-store]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/README.md?plain=1#L614-L627
[frp-readme-reload]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/README.md?plain=1#L808-L820
[frp-readme-mux]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/README.md?plain=1#L877-L886
[frp-readme-v2]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/README.md?plain=1#L141-L145
[frp-authkey]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/pkg/util/util/util.go#L50-L56
[frp-token]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/pkg/auth/token.go#L32-L90
[frp-tlsverify]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/pkg/transport/tls.go#L127-L153
[frp-serverplugin]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/doc/server_plugin.md?plain=1#L1-L16
[frp-frps-plugins]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/conf/frps_full_example.toml#L159-L169
[frp-frps-example]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/conf/frps_full_example.toml#L52-L53
[frp-frpc-example]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/conf/frpc_full_example.toml#L236-L260
[frp-conflict]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/pkg/util/vhost/router.go#L11
[frp-vhosthttp]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/pkg/util/vhost/http.go#L49-L130
[frp-sni]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/pkg/util/vhost/https.go#L39-L52
[frp-workconn]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/server/control.go#L595-L620
[frp-transport]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/pkg/config/v1/client.go#L102-L166
[frp-loginfail]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/pkg/config/v1/client.go#L54-L88
[frp-service]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/client/service.go#L276-L280
[frp-service-backoff]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/client/service.go#L300-L329
[frp-license]: https://github.com/fatedier/frp/blob/031716afd104dce6dc2cd06ecca0a76f145b744f/LICENSE
[rh-readme]: https://github.com/rathole-org/rathole/blob/a292f7ed5402f840415fc6a53827da2f34337856/README.md?plain=1#L42
[rh-readme-sec]: https://github.com/rathole-org/rathole/blob/a292f7ed5402f840415fc6a53827da2f34337856/README.md?plain=1#L35-L36
[rh-readme-transport]: https://github.com/rathole-org/rathole/blob/a292f7ed5402f840415fc6a53827da2f34337856/README.md?plain=1#L114
[rh-readme-config]: https://github.com/rathole-org/rathole/blob/a292f7ed5402f840415fc6a53827da2f34337856/README.md?plain=1#L105-L172
[rh-internals]: https://github.com/rathole-org/rathole/blob/a292f7ed5402f840415fc6a53827da2f34337856/docs/internals.md?plain=1#L27-L33
[rh-noise]: https://github.com/rathole-org/rathole/blob/a292f7ed5402f840415fc6a53827da2f34337856/docs/transport.md?plain=1#L50-L130
[rh-oos]: https://github.com/rathole-org/rathole/blob/a292f7ed5402f840415fc6a53827da2f34337856/docs/out-of-scope.md?plain=1#L9-L11
[rh-nonce]: https://github.com/rathole-org/rathole/blob/a292f7ed5402f840415fc6a53827da2f34337856/src/server.rs#L287-L321
[rh-proto]: https://github.com/rathole-org/rathole/blob/a292f7ed5402f840415fc6a53827da2f34337856/src/protocol.rs#L20-L26
[rh-backoff]: https://github.com/rathole-org/rathole/blob/a292f7ed5402f840415fc6a53827da2f34337856/src/constants.rs#L18-L25
[rh-watcher]: https://github.com/rathole-org/rathole/blob/a292f7ed5402f840415fc6a53827da2f34337856/src/config_watcher.rs#L18-L35
[rh-license]: https://github.com/rathole-org/rathole/blob/a292f7ed5402f840415fc6a53827da2f34337856/LICENSE
[in-repo]: https://github.com/inlets/inlets-pro/tree/369549b9b3a6e5cd890be461825972aaf374efe6
[in-readme]: https://github.com/inlets/inlets-pro/blob/369549b9b3a6e5cd890be461825972aaf374efe6/README.md?plain=1#L17-L21
[in-readme-license]: https://github.com/inlets/inlets-pro/blob/369549b9b3a6e5cd890be461825972aaf374efe6/README.md?plain=1#L44
[in-eula]: https://github.com/inlets/inlets-pro/blob/369549b9b3a6e5cd890be461825972aaf374efe6/EULA.md?plain=1#L3
[in-cli-topology]: https://github.com/inlets/inlets-pro/blob/369549b9b3a6e5cd890be461825972aaf374efe6/docs/cli-reference.md?plain=1#L43-L56
[in-cli-ha]: https://github.com/inlets/inlets-pro/blob/369549b9b3a6e5cd890be461825972aaf374efe6/docs/cli-reference.md?plain=1#L53-L57
[in-cli-upstreamhost]: https://github.com/inlets/inlets-pro/blob/369549b9b3a6e5cd890be461825972aaf374efe6/docs/cli-reference.md?plain=1#L79-L105
[in-cli-autotls]: https://github.com/inlets/inlets-pro/blob/369549b9b3a6e5cd890be461825972aaf374efe6/docs/cli-reference.md?plain=1#L200-L210
[in-cli-noautotls]: https://github.com/inlets/inlets-pro/blob/369549b9b3a6e5cd890be461825972aaf374efe6/docs/cli-reference.md?plain=1#L267
[in-cli-http]: https://github.com/inlets/inlets-pro/blob/369549b9b3a6e5cd890be461825972aaf374efe6/docs/cli-reference.md?plain=1#L316-L319
[in-cli-le]: https://github.com/inlets/inlets-pro/blob/369549b9b3a6e5cd890be461825972aaf374efe6/docs/cli-reference.md?plain=1#L331-L360
[in-cli-upstream]: https://github.com/inlets/inlets-pro/blob/369549b9b3a6e5cd890be461825972aaf374efe6/docs/cli-reference.md?plain=1#L362-L375
[in-manual]: https://github.com/inlets/docs.inlets.dev/blob/51fdd95093ae98f3a19f89619d90c463f91cde84/docs/tutorial/manual-http-server.md?plain=1
[in-faq-root]: https://github.com/inlets/docs.inlets.dev/blob/51fdd95093ae98f3a19f89619d90c463f91cde84/docs/reference/faq.md?plain=1#L12
[in-faq-tcp]: https://github.com/inlets/docs.inlets.dev/blob/51fdd95093ae98f3a19f89619d90c463f91cde84/docs/reference/faq.md?plain=1#L145-L151
[in-faq-planes]: https://github.com/inlets/docs.inlets.dev/blob/51fdd95093ae98f3a19f89619d90c463f91cde84/docs/reference/faq.md?plain=1#L256-L264
[in-faq-sec]: https://github.com/inlets/docs.inlets.dev/blob/51fdd95093ae98f3a19f89619d90c463f91cde84/docs/reference/faq.md?plain=1#L284-L290
[in-faq-reconnect]: https://github.com/inlets/docs.inlets.dev/blob/51fdd95093ae98f3a19f89619d90c463f91cde84/docs/reference/faq.md?plain=1#L328-L332
[in-faq-license]: https://github.com/inlets/docs.inlets.dev/blob/51fdd95093ae98f3a19f89619d90c463f91cde84/docs/reference/faq.md?plain=1#L344-L348
[ts-funnel]: https://tailscale.com/kb/1223/funnel
[ts-funnelcli]: https://tailscale.com/kb/1311/tailscale-funnel
[ts-userspace]: https://tailscale.com/kb/1112/userspace-networking
[ts-authkeys]: https://tailscale.com/kb/1085/auth-keys
[ts-keyexpiry]: https://tailscale.com/kb/1028/key-expiry
[ts-derp]: https://tailscale.com/kb/1232/derp-servers
[ts-oss]: https://tailscale.com/opensource
[hs-features]: https://headscale.net/stable/about/features/
[ts-ingress]: https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/ipn/ipnlocal/serve.go#L1495-L1560
[ts-handleingress]: https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/ipn/ipnlocal/serve.go#L453-L479
[ts-expand]: https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/ipn/serve.go#L763-L830
[ts-tsnet]: https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/tsnet/tsnet.go#L1-L25
[ts-listenfunnel]: https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/tsnet/tsnet.go#L1498-L1513
[ts-watchdog]: https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/control/controlclient/direct.go#L1015-L1018
[ts-magicsock]: https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/wgengine/magicsock/magicsock.go#L4064-L4085
[ts-license]: https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/LICENSE
