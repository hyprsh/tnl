# Can one Go binary embed Caddy and userspace WireGuard?

Research for [#3](https://github.com/hyprsh/tnl/issues/3) on the [tnl v1 map](https://github.com/hyprsh/tnl/issues/1). Researched 2026-10-15. Terms (Edge, Connector, Tunnel, Route, Hostname, Zone, Service) follow [`GLOSSARY.md`](../../GLOSSARY.md). Words like "upstream", "transport" and "handler" refer only to Caddy's own config vocabulary.

## Answer

**Yes, with caveats.** One statically linked Go binary can embed Caddy (as a library, configured with JSON through `caddy.Load`) together with `wireguard-go` and its gVisor netstack, and plain `go build` builds it. WireGuard itself needs root on neither side.

A throwaway check built exactly that ([§1.6](#16-throwaway-check)):

- an Edge process: embedded Caddy plus a netstack WireGuard device;
- a Connector process: a netstack WireGuard device only.

Both ran as an ordinary user. A request for a Hostname went through Caddy, the Tunnel and the Connector to a localhost Service. A Route added with `caddy.Load` went live while a streaming response kept flowing. The caveats are in three places: how Route changes reload Caddy, which data path the Edge uses, and how the Connector learns which Service a connection is for.

**Implications for tnl**

- **Caddy embeds cleanly, and xcaddy isn't needed.** tnl's own `main` package imports Caddy and registers any tnl-specific Caddy modules in `init()`. xcaddy only generates such a `main.go` and runs `go build -trimpath -ldflags "-w -s" -tags nobadger,nomysql,nopgx`. Caddy v2.11.7 requires Go 1.26 or newer.
- **A Route change made through Caddy's config is a full reload.** `caddy.Load` and every write to the admin API rebuild and start the *whole* config, then stop the old one.
  - Plain HTTP requests survive, because listeners overlap and the old servers drain.
  - **Upgraded connections (WebSockets) on every Route are closed by default** when the old config is cleaned up.
  - Mitigations: `stream_close_delay` (released), `stream_detached` (on Caddy master, not yet released), or keeping Route changes out of Caddy's config altogether. The last option means a tnl Caddy module (a handler, or a `dynamic_upstreams` source) that reads tnl's Route table per request, plus on-demand TLS gated by a `tls.permission` module.
  - This bears on the map's open item "how it reloads them without dropping traffic".
- **Edge option 1: netstack plus a custom `reverse_proxy` transport.**
  - Why a module is needed: Caddy's stock HTTP transport always dials with a `net.Dialer`.
  - It worked in the check: about 20 lines wrap the stock `HTTPTransport` and swap its `Transport.DialContext` for the netstack's `DialContext`. `caddy-tailscale` uses the same transport-module pattern with tsnet.
  - Pros: no `CAP_NET_ADMIN`, no interface, address or firewall management. The tunnel network exists only inside the tnl process, and the same code runs on macOS.
  - Cons:
    - userspace TCP for every proxied connection;
    - no kernel tooling;
    - it depends on a field that is exported but not a documented extension point;
    - a few stock-transport paths keep Caddy's own dialer.
- **Edge option 2: a kernel `wg0` interface.**
  - Pros: the stock `reverse_proxy` dials the Connector's tunnel address with no plugin, and kernel WireGuard is the faster path on Linux.
  - Cons:
    - needs root or `CAP_NET_ADMIN`;
    - tnl must create the interface and assign addresses itself (`wgctrl` explicitly doesn't);
    - the tunnel network becomes visible to the whole host, so firewalling `wg0` becomes tnl's job.
  - Pangolin's Gerbil works this way.
- **The Connector runs without root in every variant.** The netstack ends the Tunnel's TCP connection inside the process. The Connector then opens an ordinary OS socket to the Service (on localhost or the LAN) and copies bytes both ways. There are four realistic ways for it to learn which Service a connection is for:
  - **(A) one netstack port per Route.** This is Newt's model and needs only `wireguard-go`'s stock netstack API.
  - **(B) one port, dispatched by `Host` header** on the Connector.
  - **(C) destination-preserving forwarding**, the model of Tailscale's subnet routers. It needs a custom netstack, because `wireguard-go`'s doesn't expose its gVisor stack.
  - **(D) the target carried in-band**, for example SOCKS5 on one port.

  A and B keep Route state on the Connector; C and D need only an allowlist there.
- **Destination-preserving routing (C) breaks when LANs overlap.** WireGuard's AllowedIPs map each prefix to exactly one peer. Two Connectors that both use `192.168.1.0/24` can't both be routed by real address through one Edge device without address translation; Tailscale built "4via6" for this. Forwarding traffic for a Connector's own tunnel address to its localhost also exposes every local port unless filtered.
- **Binary size: Caddy accounts for about 50 MB, and WireGuard adds about 4 MB.** For linux/amd64, stripped:

  | Build | Size |
  | --- | --- |
  | Stock Caddy | 50.8 MB |
  | Embedded Edge with WireGuard | 54.5 MB |
  | Connector only, no Caddy | 9.0 MB |

  Importing fewer Caddy modules saved only about 3 MB. If one binary serves both roles, the Connector carries all of Caddy.
- **The licences are compatible.**
  - Caddy and gVisor are Apache-2.0, `wireguard-go` is MIT, and Tailscale is BSD-3-Clause.
  - All about 150 modules in the tagged Edge build are permissive. The untagged build adds MPL-2.0 `go-sql-driver/mysql`, which the `nomysql` tag drops.
  - A release has to ship the licence texts plus the NOTICE files of 8 Apache-licensed dependencies.
  - Newt, Gerbil and Pangolin are AGPL-3.0. They serve as evidence here; copying their code would bring the AGPL with it.

## Versions examined

| Component | Version | Source |
| --- | --- | --- |
| Caddy | v2.11.7 (latest release, 2026-10-03); master `af7620d` for unreleased features | [github.com/caddyserver/caddy](https://github.com/caddyserver/caddy/tree/v2.11.7) |
| wireguard-go | `v0.0.0-20261006164505-2631ce99a06f` (`@latest` on [proxy.golang.org](https://proxy.golang.org/golang.zx2c4.com/wireguard/@latest); no semver tags) | [WireGuard/wireguard-go@2631ce9](https://github.com/WireGuard/wireguard-go/tree/2631ce99a06f27120d581611cf125d68bc6aa565) |
| gVisor | `go` branch `ebd31f5`; wireguard-go pins `v0.0.0-20250503011706-39ed1f5ac29c` | [google/gvisor@ebd31f5](https://github.com/google/gvisor/tree/ebd31f56f21f18518797c43f5c1c8e0173718331) |
| xcaddy | master `e0e8f68` | [caddyserver/xcaddy](https://github.com/caddyserver/xcaddy/tree/e0e8f68a0f4bef11bfd04c4fb3091052ad36132f) |
| Tailscale | main `b83f28e` | [tailscale/tailscale](https://github.com/tailscale/tailscale/tree/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e) |
| caddy-tailscale | main `de41b24` | [tailscale/caddy-tailscale](https://github.com/tailscale/caddy-tailscale/tree/de41b249af4fd2083612c3b2303197e92787d1c5) |
| Newt / Gerbil / Pangolin | main `514dd0d` / `3d7c347` / `2a40040` | [fosrl/newt](https://github.com/fosrl/newt/tree/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f), [fosrl/gerbil](https://github.com/fosrl/gerbil/tree/3d7c34742f18ee039f3539d4a01daf55993ee638), [fosrl/pangolin](https://github.com/fosrl/pangolin/tree/2a4004044d8291fd66b1ac2202c49628de0a95ff) |
| Go (throwaway check only) | 1.27.2 | [go.dev/dl](https://go.dev/dl/) |

## 1. Caddy as a library

### 1.1 Embedding and building

- Caddy's own `main.go` does three things: it blank-imports `modules/standard`, imports `caddycmd` and calls `caddycmd.Main()`. Its header comment describes a custom build as: copy the file, edit the imports, then `go mod init` and `go build` ([`cmd/caddy/main.go` L14-L42](https://github.com/caddyserver/caddy/blob/v2.11.7/cmd/caddy/main.go#L14-L42)). Modules are "plugged in" by adding an `import`, and Caddy is "a single, self-contained, static binary" ([Architecture docs](https://caddyserver.com/docs/architecture)).
- Embedding without the CLI uses `caddy.Run(*caddy.Config)` or `caddy.Load(cfgJSON []byte, forceReload bool)` ([`caddy.go` L103-L148](https://github.com/caddyserver/caddy/blob/v2.11.7/caddy.go#L103-L148); [pkg.go.dev](https://pkg.go.dev/github.com/caddyserver/caddy/v2#Load)). tnl's own modules register with `caddy.RegisterModule` from `init()` ([`modules.go` L138](https://github.com/caddyserver/caddy/blob/v2.11.7/modules.go#L138)).
- **xcaddy is a convenience wrapper, not a requirement.** It generates a `main.go`, runs `go mod tidy`, then runs `go build -o … -ldflags "-w -s" -trimpath -tags nobadger,nomysql,nopgx` ([`builder.go` L466-L499](https://github.com/caddyserver/xcaddy/blob/e0e8f68a0f4bef11bfd04c4fb3091052ad36132f/builder.go#L466-L499)). The tags remove optional database drivers from `smallstep/nosql`, which Caddy's PKI pulls in. For example, its MySQL backend is guarded by `//go:build !nomysql` ([`mysql/mysql.go` L1](https://github.com/smallstep/nosql/blob/v0.8.0/mysql/mysql.go#L1)).
- **Toolchain floor.** Caddy v2.11.7 declares `go 1.26.0` ([`go.mod` L3](https://github.com/caddyserver/caddy/blob/v2.11.7/go.mod#L3)) and `wireguard-go` declares `go 1.23.1` ([`go.mod` L3](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/go.mod#L3)), so tnl needs Go 1.26 or newer.
- **gVisor comes from its synthetic `go` branch.** `@latest` resolves `master`, which needs Bazel ([gVisor README, "Using `go get`"](https://github.com/google/gvisor/blob/master/README.md#using-go-get)). `wireguard-go` pins a `go`-branch pseudo-version ([`go.mod` L10](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/go.mod#L10)), and `go get` of both modules resolved cleanly in the throwaway check.

### 1.2 Loading config programmatically

- `caddy.Load` calls `changeConfig(POST, "/config", …)`, the same function the admin API uses. When the new config is byte-identical to the current one and `forceReload` is false, nothing happens ([`caddy.go` L115-L148](https://github.com/caddyserver/caddy/blob/v2.11.7/caddy.go#L115-L148), [L158-L215](https://github.com/caddyserver/caddy/blob/v2.11.7/caddy.go#L158-L215)).
- The admin API offers `POST /load`, `POST/PUT/PATCH/DELETE /config/[path]`, `@id` shortcuts, and optimistic concurrency through `Etag`/`If-Match` ([API docs](https://caddyserver.com/docs/api)).
- **The admin endpoint can be turned off.** Its doc comment says turning it off "makes any runtime changes to the config impossible, since the interface to do so is through the admin endpoint" ([`admin.go` L70-L77](https://github.com/caddyserver/caddy/blob/v2.11.7/admin.go#L70-L77)). In the throwaway check, though, an in-process `caddy.Load` still applied a new config with `"admin": {"disabled": true}`. The restriction applies to the external API, not to in-process calls. When left enabled, the endpoint listens on `localhost:2019` by default ([`admin.go` L1455](https://github.com/caddyserver/caddy/blob/v2.11.7/admin.go#L1455)).
- **Caddy writes pushed configs to disk by default.** Every pushed config is autosaved to `autosave.json` ([`caddy.go` L380-L398](https://github.com/caddyserver/caddy/blob/v2.11.7/caddy.go#L380-L398), [`storage.go` L157](https://github.com/caddyserver/caddy/blob/v2.11.7/storage.go#L157)), and `"admin": {"config": {"persist": false}}` turns this off ([`admin.go` L125-L131](https://github.com/caddyserver/caddy/blob/v2.11.7/admin.go#L125-L131)). This matters because the map makes the Edge's own store the single source of truth for Routes, and Caddy's autosave would be a second copy.

### 1.3 What a reload does

- **Every change is a full reload.**
  1. `changeConfig` applies the change to the raw config, then re-encodes the **entire** config ([`caddy.go` L208-L215](https://github.com/caddyserver/caddy/blob/v2.11.7/caddy.go#L208-L215)).
  2. `unsyncedDecodeAndRun` calls `run(newCfg, true)`, which provisions and starts every app.
  3. It then swaps the context and calls `unsyncedStop(oldCtx)`, which stops every old app and cancels its modules ([L337-L379](https://github.com/caddyserver/caddy/blob/v2.11.7/caddy.go#L337-L379), [L722-L740](https://github.com/caddyserver/caddy/blob/v2.11.7/caddy.go#L722-L740)).

  So a `PATCH` to a single Route still restarts the whole `http` and `tls` apps.
- The docs describe this as "lightweight, efficient, and incur zero downtime" ([API docs](https://caddyserver.com/docs/api)); "for a brief period, two configs are operational at the same time" ([Architecture docs](https://caddyserver.com/docs/architecture)).
- **Listeners overlap.** On Unix, Caddy sets `SO_REUSEPORT` and counts how many configs use each socket, so the new config can bind while the old one still holds the port ([`listeners.go` L103-L135](https://github.com/caddyserver/caddy/blob/v2.11.7/listeners.go#L103-L135), [`listen_unix.go` L107-L112](https://github.com/caddyserver/caddy/blob/v2.11.7/listen_unix.go#L107-L112)).
- **Old servers drain.** Each old HTTP server gets `http.Server.Shutdown(ctx)`. During the grace period, no new connections are accepted, idle connections are closed, and active ones may finish. `grace_period` defaults to 0, which means an eternal grace period ([`modules/caddyhttp/app.go` L136-L155](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L136-L155), [L718-L795](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L718-L795)). Frequent Route changes therefore leave old server instances alive until their last connection ends, unless a grace period is set.
- **Certificates survive reloads.** The TLS app keeps one package-level certmagic cache across config loads and only updates its options ([`modules/caddytls/tls.go` L47-L49](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/tls.go#L47-L49), [L218-L224](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/tls.go#L218-L224)).

### 1.4 What a reload drops

- **Upgraded connections close on cleanup.** When the old config is cleaned up, `reverse_proxy`'s `Cleanup` calls `cleanupConnections`. With `stream_close_delay` at its default of 0, that closes every hijacked (upgraded) connection immediately ([`reverseproxy.go` L458-L467](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go#L458-L467), [`streaming.go` L460-L490](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/streaming.go#L460-L490)). Since a reload cleans up every handler in the old config, **a change to one Route closes WebSockets on all Routes.**
- **`stream_close_delay` postpones the close** by the configured delay ([`reverseproxy.go` L193-L200](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go#L193-L200)).
- **`stream_detached` (master only) keeps them open.** It isn't in v2.11.7; on master it "detache[s] … upgraded connections" and retains them across reloads when their upstream still exists, for HTTP/1.1 WebSockets only ([master `reverseproxy.go` L205-L215](https://github.com/caddyserver/caddy/blob/af7620dcc8a3b2edeaa8adc5d2850115286f4658/modules/caddyhttp/reverseproxy/reverseproxy.go#L205-L215)).
- **Ordinary requests in flight are not dropped.** The old server drains them ([§1.3](#13-what-a-reload-does)), as the check confirmed for a streaming response.

### 1.5 Changing Routes without reloading Caddy

Caddy has extension points that are consulted **per request**, so a tnl module can read tnl's own Route table instead of encoding Routes as Caddy config:

- **`dynamic_upstreams`.** These modules live in the `http.reverse_proxy.upstreams` namespace and "are retrieved at every iteration of the proxy loop for each request". Active health checks don't work with them ([`reverseproxy.go` L122-L131](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go#L122-L131)). The interface is `GetUpstreams(*http.Request) ([]*Upstream, error)`, and its doc comment asks for it to be "instantaneous" ([L1745-L1760](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go#L1745-L1760)).
- **Custom HTTP handlers.** A handler module implements `caddyhttp.MiddlewareHandler` ([`caddyhttp.go` L81-L95](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/caddyhttp.go#L81-L95)).
- **On-demand TLS** obtains a certificate "during the first TLS handshake that requires it, rather than at config load", and "must be both enabled and restricted to prevent abuse" ([Automatic HTTPS docs](https://caddyserver.com/docs/automatic-https)). A `tls.permission` module implements `CertificateAllowed(ctx, name)`, which Caddy checks before obtaining or loading a certificate ([`modules/caddytls/ondemand.go` L40-L72](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/ondemand.go#L40-L72)). With a static config, by contrast, Caddy manages certificates in the background for every qualifying name it finds in the config ([Automatic HTTPS docs](https://caddyserver.com/docs/automatic-https)).
- **Handing tnl state to Caddy modules.** Caddy creates modules from JSON, so in-process objects such as the netstack or the Route table have to reach them through a package-level registry. `caddy-tailscale` keeps its tsnet nodes in a global `caddy.UsagePool` ([`module.go` L245-L262](https://github.com/tailscale/caddy-tailscale/blob/de41b249af4fd2083612c3b2303197e92787d1c5/module.go#L245-L262); [`usagepool.go` L23-L50](https://github.com/caddyserver/caddy/blob/v2.11.7/usagepool.go#L23-L50)). The throwaway check used a package variable.

### 1.6 Throwaway check

Not committed. It used Go 1.27.2, Caddy v2.11.7 and `wireguard-go` `2631ce9`, ran on an Apple M1 under macOS, and ran as uid 501 (not root).

**Edge process**
- A `wireguard-go` netstack device at 10.66.0.1, on UDP port 51820.
- Embedded Caddy with `"admin": {"disabled": true}` and one server on `:8080`.
- Route `app.test`: `reverse_proxy` through transport `tnl` to `10.66.0.2:80`.

**Connector process**
- A netstack device at 10.66.0.2, configured with the Edge's endpoint `127.0.0.1:51820` and a persistent keepalive.
- `tnet.ListenTCP(:80)` forwards to `net.Dial("127.0.0.1:9000")`, and `:81` forwards to `127.0.0.1:9001`.

The whole Edge-side Caddy module:

```go
type TnlTransport struct{ reverseproxy.HTTPTransport }

func (TnlTransport) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "http.reverse_proxy.transport.tnl",
		New: func() caddy.Module { return new(TnlTransport) }}
}

func (t *TnlTransport) Provision(ctx caddy.Context) error {
	if err := t.HTTPTransport.Provision(ctx); err != nil {
		return err
	}
	t.HTTPTransport.Transport.DialContext = edgeNet.DialContext // *netstack.Net
	return nil
}
```

**Observed**
- `GET Host: app.test` returned the Service's response. The Service saw its client as `127.0.0.1:…`, which is the Connector.
- `caddy.Load` with a second Route (`app2.test` → `10.66.0.2:81`) took 0.9 ms, after which `app2.test` answered.
- An 8-second streaming response started before the reload delivered all 8 chunks, one per second, across it. Caddy logged "servers shutting down with eternal grace period".
- Rough throughput: 512 MiB through Caddy → netstack WireGuard → netstack → Service, on one machine over loopback UDP, ran at about 51–73 MB/s (≈ 400–580 Mbit/s). The same download direct from the Service ran at about 6.7 GB/s. This is an indication only, with userspace stacks at both ends. It is not a Linux VPS benchmark.

## 2. The Edge side of the data path

### 2.1 Option E1: netstack on the Edge, plus a custom transport

- **The netstack API.** `netstack.CreateNetTUN(localAddresses, dnsServers, mtu)` builds a private gVisor stack. It returns a `tun.Device` for `device.NewDevice` and a `*netstack.Net` with `DialContext`, `ListenTCP` and related methods ([`tun/netstack/tun.go` L55-L109](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/tun/netstack/tun.go#L55-L109), [L957](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/tun/netstack/tun.go#L957); [pkg.go.dev](https://pkg.go.dev/golang.zx2c4.com/wireguard/tun/netstack)). Upstream ships a runnable example ([`examples/http_server.go`](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/tun/netstack/examples/http_server.go)).
- **Dialing.** `DialContext(ctx, "tcp", "10.66.0.2:80")` takes IP literals without DNS ([L823-L825](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/tun/netstack/tun.go#L823-L825)) and dials through gVisor's `gonet.DialContextTCP` ([L208-L211](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/tun/netstack/tun.go#L208-L211)).
- **Why a custom transport is needed.**
  - Caddy's stock HTTP transport builds a `net.Dialer` and calls `dialer.DialContext`, with no hook for a different dialer ([`httptransport.go` L228-L231](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/httptransport.go#L228-L231), [L277-L292](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/httptransport.go#L277-L292)).
  - `caddy.RegisterNetwork` adds custom network types for **listeners** only ([`listeners.go` L662-L679](https://github.com/caddyserver/caddy/blob/v2.11.7/listeners.go#L662-L679)).
  - The transport is itself a module, in namespace `http.reverse_proxy.transport`, and must be an `http.RoundTripper` ([`reverseproxy.go` L105](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go#L105), [L302](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go#L302)).
- **Two ways to write it.**
  - *Own RoundTripper.* `caddy-tailscale` registers `http.reverse_proxy.transport.tailscale`, which hands every request to the tsnet node's HTTP client ([`transport.go` L20-L36, L73-L82](https://github.com/tailscale/caddy-tailscale/blob/de41b249af4fd2083612c3b2303197e92787d1c5/transport.go#L20-L82)). This is simple, but it loses the stock transport's options: timeouts, keepalive tuning, TLS to the Service, PROXY protocol.
  - *Wrap the stock transport* (the check above). `HTTPTransport.Transport` is an exported `*http.Transport` tagged `json:"-"`, set during `Provision`, and `RoundTrip` uses it ([`httptransport.go` L146-L147](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/httptransport.go#L146-L147), [L184-L202](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/httptransport.go#L184-L202), [L643-L653](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/httptransport.go#L643-L653)). Replacing its `DialContext` keeps the other options. Caveats:
    - The field is exported but not a documented extension point.
    - Paths that capture Caddy's internal dialer bypass the swap: a custom `DialTLSContext` used when the TLS server name holds placeholders or PROXY protocol is on ([L429-L436](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/httptransport.go#L429-L436)), and the experimental HTTP/3 transport ([L504-L520](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/httptransport.go#L504-L520)).
- **Many Connectors on one device.**
  - One device holds many peers on one UDP port.
  - Peers can be added, changed or removed live through UAPI strings passed to `IpcSet`: `public_key=`, `allowed_ip=`, `replace_allowed_ips=`, `remove=true` ([`device/uapi.go` L236-L373, L415](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/uapi.go#L236-L417)).
  - The Edge doesn't need to know a Connector's endpoint in advance: "the server discovers the endpoint of its peers by examining from where correctly authenticated data originates" ([WireGuard, Built-in Roaming](https://www.wireguard.com/#built-in-roaming)).
- **Performance signals.**
  - The netstack TUN moves one packet per `Read`/`Write` batch (`BatchSize()` returns 1) ([L123-L192](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/tun/netstack/tun.go#L123-L192)). The Linux kernel-TUN path supports TSO/USO offloads with larger batches ([`tun_linux.go` L501-L508](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/tun/tun_linux.go#L501-L508)).
  - `wireguard-go`'s README says that on Linux "you should instead use the kernel module, which is faster and better integrated" ([README L29](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/README.md?plain=1#L29)).
  - Tailscale calls userspace mode "more than sufficient for smaller numbers of users or low bandwidth" but "not as optimized as the Linux kernel" ([Tailscale KB 1177](https://tailscale.com/kb/1177/kernel-vs-userspace-routers)).
- **Isolation.** Tunnel addresses live only in the process's private gVisor stack, and the host gets no route to them. Other host processes can't reach Connectors, and Connectors reach only what tnl listens on inside the netstack.
- **Privileges.** WireGuard needs none here. Binding 80/443 still needs root or `CAP_NET_BIND_SERVICE` ([capabilities(7)](https://man7.org/linux/man-pages/man7/capabilities.7.html)), which the map already allows.

### 2.2 Option E2: a kernel `wg0` interface on the Edge

- **No Caddy plugin needed.** Linux has had WireGuard in the kernel since 5.6 ([kernelnewbies: Linux 5.6](https://kernelnewbies.org/Linux_5.6)). Caddy's stock `reverse_proxy` dials `10.x.y.z:port`, and the kernel routes the connection through `wg0`.
- **What tnl must manage:**
  - Create the interface, the equivalent of `ip link add wg0 type wireguard` ([wireguard-go README L7](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/README.md?plain=1#L7)), over netlink.
  - Assign its address.
  - Configure keys and peers. `wgctrl` (MIT) configures existing kernel devices over generic netlink, but says "creating WireGuard devices, or applying IP addresses to those devices, are out of scope" ([wgctrl-go README](https://github.com/WireGuard/wgctrl-go/blob/a9ab2273dd1075ea74b88c76f8757f8b4003fcbf/README.md)).

  Network interface configuration needs `CAP_NET_ADMIN` ([capabilities(7)](https://man7.org/linux/man-pages/man7/capabilities.7.html)).
- **Evidence from Pangolin.** Gerbil, Pangolin's server component, "create[s] WireGuard interfaces as well as add[s] and remove[s] peers with an HTTP API", defaults to `wg0`, and ships firewall rules. Its `disable-firewall` option is described as "allowing all inbound traffic on the interface" ([Gerbil README L3, L15-L19, L71-L84](https://github.com/fosrl/gerbil/blob/3d7c34742f18ee039f3539d4a01daf55993ee638/README.md?plain=1#L3-L84)). It depends on `wgctrl` and `vishvananda/netlink` ([`go.mod`](https://github.com/fosrl/gerbil/blob/3d7c34742f18ee039f3539d4a01daf55993ee638/go.mod)). Pangolin's Traefik then proxies to `http://<Newt site IP>:<internalPort>` ([`loadBalancer.ts` L53-L57](https://github.com/fosrl/pangolin/blob/2a4004044d8291fd66b1ac2202c49628de0a95ff/server/lib/traefik/loadBalancer.ts#L53-L57)).
- **Exposure.** The tunnel subnet becomes a host network. Any Edge service bound to all addresses is reachable from Connectors, and every host process can reach Connectors, unless tnl firewalls `wg0`. That is why Gerbil ships firewall rules.
- **Variant: `wireguard-go` on a kernel TUN.** This gives userspace crypto with the kernel's TCP stack. It still needs a TUN device, so `CAP_NET_ADMIN`, and the README recommends the kernel module on Linux anyway.

### 2.3 Edge trade-offs at a glance

| | E1: netstack + transport module | E2: kernel `wg0` |
| --- | --- | --- |
| Caddy change | ~20-line `http.reverse_proxy.transport` module | none |
| Privileges for WireGuard | none | root / `CAP_NET_ADMIN`, kernel ≥ 5.6 |
| What tnl manages | UAPI peers in-process | interface, address, peers (`wgctrl`), firewall |
| Tunnel network visible to host | no | yes |
| Speed | userspace TCP + crypto | kernel |
| Tooling (`ip`, `tcpdump`, `wg`) | not directly; `wg` would need tnl to expose a UAPI socket ([`IpcHandle`](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/uapi.go#L419)) | yes |
| Same code as Connector / runs on macOS | yes | no |

## 3. The Connector side, without root

### 3.1 What runs unprivileged

- **WireGuard's UDP socket is ordinary.** The default bind opens it with `net.ListenConfig.ListenPacket` ([`conn/bind_std.go` L122-L123](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/conn/bind_std.go#L122-L123)). The only socket option that needs `CAP_NET_ADMIN` (`SO_*BUFFORCE`) "is allowed here to fail silently", at a cost to performance on fast links ([`conn/controlfns_linux.go` L46-L59](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/conn/controlfns_linux.go#L46-L59)).
- **The Connector only dials out.** It names the Edge as its endpoint and keeps the NAT mapping alive with `persistent_keepalive_interval` ([WireGuard quickstart: NAT and firewall traversal persistence](https://www.wireguard.com/quickstart/#nat-and-firewall-traversal-persistence)).
- **Accepting connections from the Tunnel.** `tnet.ListenTCP(&net.TCPAddr{Port: p})` returns a `*gonet.TCPListener` on the Connector's tunnel address, inside its netstack ([`tun.go` L234-L245](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/tun/netstack/tun.go#L234-L245)).
- **Forwarding to the Service.** The Connector calls plain `net.Dial` to reach the Service on localhost or the LAN, then copies both ways, half-closing with `gonet.TCPConn.CloseWrite` ([gonet.go L457](https://github.com/google/gvisor/blob/ebd31f56f21f18518797c43f5c1c8e0173718331/pkg/tcpip/adapters/gonet/gonet.go#L457)).
  - Newt does exactly this ([`proxy/manager.go` L555-L575, L658](https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/proxy/manager.go#L555-L658)).
  - So does Tailscale. Tailscale also dials the Service *before* completing the incoming TCP handshake, so an unreachable Service turns into a RST ([`wgengine/netstack/netstack.go` L1826-L1900](https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/wgengine/netstack/netstack.go#L1826-L1900)).
- **Tailscale's tsnet** embeds the same idea as a library: "No root privileges required", with `Server.Listen` and `Server.Dial` returning standard `net.Listener` and `net.Conn` values ([tsnet README](https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/tsnet/README.md); [`tsnet.go` L373, L1313](https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/tsnet/tsnet.go#L1313)).

### 3.2 How the Connector learns which Service a connection is for

**(A) One netstack port per Route.**
- The Edge dials `<Connector tunnel IP>:<Route port>`, and the Connector maps each port to a Service address.
- It needs nothing beyond the stock `wireguard-go` netstack API.
- This is Newt's model. Newt is "a fully user space WireGuard tunnel client and TCP/UDP proxy" ([README L9-L29](https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/README.md?plain=1#L9-L29)). It calls `netstack.CreateNetTUN` ([`newt/connect.go` L105-L119](https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/newt/connect.go#L105-L119)) and opens one `tnet.ListenTCP` per target ([`proxy/manager.go` L555-L575](https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/proxy/manager.go#L555-L575)).
- The Connector has to receive the Route-to-port table and open or close listeners as Routes change.

**(B) One port, dispatched by `Host` header.**
- The Edge sends every request to `<Connector tunnel IP>:80` with the Hostname intact.
- The Connector runs an HTTP reverse proxy (for example `httputil.ReverseProxy`) that picks the Service by `Host`.
- This parses HTTP twice, and the Connector needs the Hostname-to-Service table.

**(C) Destination-preserving forwarding.** This is the model of Tailscale's subnet routers.
- **How it works.** The Edge dials the Service's real address (for example `192.168.1.10:8080`) through the Tunnel. The Connector's gVisor stack accepts connections to *any* destination and dials that destination with an OS socket.
- **gVisor primitives.** It is built from `Stack.SetPromiscuousMode` and `Stack.SetSpoofing` ([`stack.go` L1866-L1892](https://github.com/google/gvisor/blob/ebd31f56f21f18518797c43f5c1c8e0173718331/pkg/tcpip/stack/stack.go#L1866-L1892)), plus `tcp.NewForwarder` installed with `SetTransportProtocolHandler` ([`forwarder.go` L42-L66](https://github.com/google/gvisor/blob/ebd31f56f21f18518797c43f5c1c8e0173718331/pkg/tcpip/transport/tcp/forwarder.go#L42-L66), [`stack.go` L597](https://github.com/google/gvisor/blob/ebd31f56f21f18518797c43f5c1c8e0173718331/pkg/tcpip/stack/stack.go#L597)).
- **Tailscale's version.** Tailscale enables promiscuous mode because "WireGuard will only send us packets that are meant for us" ([`netstack.go` L414-L419](https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/wgengine/netstack/netstack.go#L414-L419), [L664-L667](https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/wgengine/netstack/netstack.go#L664-L667)). It rewrites connections aimed at its own tunnel IP to `127.0.0.1` ([L1790-L1791](https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/wgengine/netstack/netstack.go#L1790-L1791)). Its docs say userspace subnet routing "terminates TCP and UDP connections" and makes "new outbound connections to the target", with no relaying of other ICMP or non-TCP/UDP protocols ([KB 1177](https://tailscale.com/kb/1177/kernel-vs-userspace-routers)).
- **Newt's version.** Newt carries a fork (`netstack2`) with promiscuous mode and spoofing on, and a forwarder that dials the target ([`netstack2/proxy.go` L486-L493](https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/netstack2/proxy.go#L486-L493), [`handlers.go` L102-L125, L204-L207](https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/netstack2/handlers.go#L102-L207)).
- **Needs its own netstack device.** `wireguard-go`'s netstack keeps its gVisor stack in an unexported field and exposes no forwarder hook ([`tun.go` L42-L53](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/tun/netstack/tun.go#L42-L53)). tnl would need its own `tun.Device` on a gVisor channel endpoint, roughly the first 200 lines of that file, which are MIT.
- **Upside.** The Connector holds no per-Route state, only an allowlist of what it will dial.
- **Overlapping LANs collide.**
  - The Edge has to route each Connector's LAN prefixes to that peer through AllowedIPs. In WireGuard each allowed IP selects "which peer to send it to" ([WireGuard, Cryptokey Routing](https://www.wireguard.com/#cryptokey-routing)).
  - Inserting an exact prefix already held by another peer moves it to the new peer ([`device/allowedips.go` L115-L120](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/allowedips.go#L115-L120)). Two Connectors on `192.168.1.0/24` therefore can't both be routed by real address on one device.
  - Tailscale's answer is 4via6, which encodes a site ID plus the IPv4 address in an IPv6 address ([KB 1201](https://tailscale.com/kb/1201/4via6-subnets)).
- **Localhost exposure.** Rewriting the Connector's own tunnel address to localhost exposes every local port unless filtered.

**(D) Target carried in-band.**
- The Connector runs a SOCKS5 or HTTP CONNECT server (or a tnl-specific header protocol) on one netstack port, and the Edge names the target per connection.
- Caddy's HTTP transport takes a `network_proxy` module ([`httptransport.go` L163](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/httptransport.go#L163)).
- Go's `http.Transport` dials the proxy with its own dial function, then runs the SOCKS5 handshake over that connection ([`net/http/transport.go` L1920, L1940-L1956](https://github.com/golang/go/blob/go1.27.2/src/net/http/transport.go#L1920-L1956)). It therefore composes with either E1 or E2.
- The Connector needs only an allowlist. Overlapping LANs aren't a problem, because the target never appears in AllowedIPs.

### 3.3 Connector trade-offs at a glance

| | A: port per Route | B: Host dispatch | C: destination-preserving | D: in-band target |
| --- | --- | --- | --- | --- |
| Stock `wireguard-go` netstack API | yes | yes | no (own netstack device) | yes |
| Route state on Connector | Route → port | Hostname → Service | allowlist only | allowlist only |
| Edge-side Caddy config per Route | Connector IP:port | Connector IP:80 + Host | Service's real address | proxy address + Service |
| Overlapping LANs across Connectors | fine | fine | collides in AllowedIPs | fine |
| Prior art | Newt / Pangolin | — | Tailscale subnet router, Newt `netstack2` | Caddy `network_proxy` + Go SOCKS5 |

In every variant the Connector reaches the Service with an ordinary outbound socket, so it needs no root.

## 4. Practical costs

### 4.1 Binary size

Built with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` and Go 1.27.2. "Stripped" means xcaddy's flags: `-trimpath -ldflags "-w -s" -tags nobadger,nomysql,nopgx`.

| Binary | Default flags | Stripped |
| --- | ---: | ---: |
| Official Caddy v2.11.7 release ([asset](https://github.com/caddyserver/caddy/releases/tag/v2.11.7)) | — | 51,953,826 B |
| Stock `cmd/caddy` main, all standard modules | 80,817,550 B | 50,798,752 B |
| Embedded Caddy (`caddy.Load`, no CLI), standard modules | 80,407,449 B | 50,389,152 B |
| **Embedded Caddy + `wireguard-go` netstack + transport module (Edge)** | 86,323,255 B | **54,468,768 B** |
| Same, hand-picked modules (`caddyhttp`, `reverseproxy`, `caddytls`, `filestorage`, `logging`) | — | 51,544,224 B |
| **`wireguard-go` netstack + forwarder only (Connector)** | 13,325,710 B | **9,027,744 B** |

WireGuard and gVisor add about 4 MB to Caddy. Trimming Caddy's imports saves about 3 MB, because `caddytls` still pulls in smallstep's PKI, quic-go and OpenTelemetry.

### 4.2 Build tooling

- **Plain `go build` is enough.** The check used no xcaddy. Copying xcaddy's flags reproduces its output size, and tnl's Caddy modules can live in tnl's own packages ([§1.1](#11-embedding-and-building)).
- **Version pinning.**
  - Neither `wireguard-go` nor gVisor's `go` branch has semver tags, so both are pinned by pseudo-version.
  - Keeping gVisor at the version `wireguard-go` pins avoids API skew between the two.
  - Caddy sets the Go floor at 1.26.

### 4.3 Licences

| Component | Licence | Source |
| --- | --- | --- |
| Caddy | Apache-2.0 | [LICENSE](https://github.com/caddyserver/caddy/blob/v2.11.7/LICENSE) |
| wireguard-go | MIT | [LICENSE](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/LICENSE) |
| gVisor | Apache-2.0 | [LICENSE](https://github.com/google/gvisor/blob/ebd31f56f21f18518797c43f5c1c8e0173718331/LICENSE) |
| wgctrl-go | MIT | [README](https://github.com/WireGuard/wgctrl-go/blob/a9ab2273dd1075ea74b88c76f8757f8b4003fcbf/README.md) |
| Tailscale (incl. netstack code, tsnet) | BSD-3-Clause | [LICENSE](https://github.com/tailscale/tailscale/blob/b83f28ebfbd9f86c2543e30e9dcf035a417dce1e/LICENSE) |
| caddy-tailscale | Apache-2.0 | [LICENSE](https://github.com/tailscale/caddy-tailscale/blob/de41b249af4fd2083612c3b2303197e92787d1c5/LICENSE) |
| Newt, Gerbil (Pangolin) | AGPL-3.0, plus a commercial licence | [Newt README L53](https://github.com/fosrl/newt/blob/514dd0dec1ca95aca7a8dc5387bb2e58f1f05d1f/README.md?plain=1#L53), [Gerbil README L164](https://github.com/fosrl/gerbil/blob/3d7c34742f18ee039f3539d4a01daf55993ee638/README.md?plain=1#L164) |

- **The tagged Edge build is all permissive.** A scan of the licence files of all 150 modules linked into the stripped Edge binary (`go version -m`) found 69 Apache-2.0, 31 BSD, 42 MIT, 7 more MIT/Apache files the scan missed (checked by hand), and one CC0 (`zeebo/blake3`). There was no GPL, LGPL, AGPL or MPL. tnl can therefore use any licence, including Apache-2.0 or MIT.
- **The untagged build adds one MPL-2.0 module.** It links `go-sql-driver/mysql` ([LICENSE](https://github.com/go-sql-driver/mysql/blob/v1.10.1/LICENSE)), which is file-level copyleft ([MPL 2.0 FAQ](https://www.mozilla.org/en-US/MPL/2.0/FAQ/)). The `nomysql` tag drops it.
- **Obligations.** Ship the licence texts. Apache-2.0 §4(d) also asks redistributors to include the content of upstream NOTICE files ([Apache-2.0](https://www.apache.org/licenses/LICENSE-2.0#redistribution)). In the tagged build, those are `prometheus/{client_golang,client_model,common,procfs}`, `google.golang.org/grpc`, `coreos/go-oidc`, `gopkg.in/yaml.v3` and `go.yaml.in/yaml/v3`. Caddy, gVisor and `wireguard-go` have no NOTICE file.
- **Copying code.** Tailscale's netstack code (BSD-3) could be borrowed with attribution. Copying Newt's `netstack2` or Gerbil code would bring the AGPL with it, unless tnl itself adopts the AGPL.

## 5. Questions this raises

These go to downstream tickets; this report doesn't decide them.

- **Data path ([#5](https://github.com/hyprsh/tnl/issues/5)).**
  - Edge: E1 or E2 ([§2.3](#23-edge-trade-offs-at-a-glance)).
  - Connector: A, B, C or D ([§3.3](#33-connector-trade-offs-at-a-glance)), and therefore whether the Connector holds Route state or only an allowlist.
- **Reloading without dropping traffic (open item on the map).** Should Routes be Caddy config, with a reload per change that closes all WebSockets by default, or live in tnl's own store and be read by a per-request Caddy module with on-demand TLS?
- **One binary for both roles.** It makes the Connector about 54 MB instead of about 9 MB. Build tags could split the roles from one codebase.
- **HTTP/3 and UDP 443.** Caddy's default server protocols are `h1 h2 h3` ([`server.go` L344-L345, L543](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/server.go#L344-L345)), and h3 uses UDP 443. The map's Edge opens TCP 80/443 and one UDP port, for WireGuard. Either UDP 443 is opened as well, or the Edge sets `protocols` to `h1 h2`.
- **Overlapping Connector LANs** become a constraint only under option C.
