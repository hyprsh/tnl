# Timeouts and buffering from visitor to Service

Research for [#10](https://github.com/hyprsh/tnl/issues/10) on the [tnl v1 map](https://github.com/hyprsh/tnl/issues/1). Researched 2026-10-15. Terms (Edge, Outpost, Tunnel, Route, Hostname, Zone, Service) follow [`GLOSSARY.md`](../../GLOSSARY.md). Words like "upstream", "transport" and "handler" refer only to Caddy's own config vocabulary.

The path is the one decided in [Data path: how a request travels from Edge to Service](https://github.com/hyprsh/tnl/issues/5): visitor → embedded Caddy (HTTP/1.1, HTTP/2 or HTTP/3) → tnl Caddy module → `reverse_proxy` with tnl's own `http.RoundTripper` → Edge netstack → WireGuard → Outpost netstack → the Outpost's byte copy → Service.

## Answer

**Nothing on the path buffers an event or cuts a stream or a 250 MB upload with Caddy v2.11.7's defaults, provided tnl pins Caddy v2.11.7 or newer, leaves a handful of settings at their defaults, and gives the Outpost a persistent keepalive.** Two caveats remain. Over HTTP/2, an upload fails with 502 if the Service stops reading the body for more than a minute. A Caddy reload keeps hour-long streams open only while `grace_period` stays at 0, its default. Any finite grace period cuts HTTP/3 streams when it ends.

A throwaway check ([§10](#10-throwaway-check)) ran the whole path in one process: embedded Caddy, two `wireguard-go` netstacks and an Outpost-style byte copy. It shrank Caddy's timeouts to 2 s so that pauses longer than every timeout fit into seconds. It confirmed each of these results over HTTP/1.1, HTTP/2 and HTTP/3.

**Implications for tnl**

- **Pin Caddy v2.11.7 or newer.** v2.11.6 (2026-10-01) added default 1-minute idle read and write timeouts, and they broke streams. Over HTTP/2, an SSE stream that paused for longer than a minute between writes was reset. Over HTTP/1.1, an SSE stream opened with a `POST` was cut 60 s after its body had been read. v2.11.7 fixes both ([release notes](https://github.com/caddyserver/caddy/releases/tag/v2.11.7); [#8118](https://github.com/caddyserver/caddy/issues/8118), [#8103](https://github.com/caddyserver/caddy/issues/8103)).
- **What tnl must set, or leave alone:**

  | Where | Setting | Value | Why |
  | --- | --- | --- | --- |
  | Caddy `http` server | `read_timeout`, `write_timeout` | 0, the default | Either is a hard ceiling on every request, so any value cuts a long stream. |
  | Caddy `http` server | `idle_timeout` (5 min), `read_header_timeout` (1 min) | defaults | They never apply to a request in progress. |
  | Caddy `http` server | `write_idle_timeout` (1 min) | default | It fires only when one write makes no progress for a minute, i.e. the visitor stopped reading. Pauses between events don't count. |
  | Caddy `http` server | `read_idle_timeout` (1 min) | default, or longer if a Service may stop reading a body for more than a minute | Over HTTP/2 the timer runs even while nothing is reading, so a stalled Service makes the upload fail with 502. |
  | Caddy `http` app | `grace_period` | 0, the default (eternal) | A finite value cuts HTTP/3 streams when the old config's grace period ends. HTTP/1.1 and HTTP/2 streams will be cut too once [#8078](https://github.com/caddyserver/caddy/issues/8078) is fixed. |
  | Caddy handler chain | `encode` | leave it out | Since v2.11.6 it no longer holds events back, but it compresses `text/event-stream` and flushes the compressor on every event. Services can compress themselves. |
  | `reverse_proxy` | `flush_interval`, `request_buffers`, `response_buffers` | defaults | `text/event-stream` is always flushed immediately, and bodies stream through unbuffered. |
  | tnl's `RoundTripper` | `http.Transport` fields | HTTP/1.1 only, `DisableCompression: true`, no `ResponseHeaderTimeout` (or several minutes), `IdleConnTimeout` around 90 s, dial bounded by the request context | Section [§5](#5-tnls-roundtripper-over-netstack). |
  | tnl's Caddy modules | `Cleanup()` | release idle state only | A reload calls `Cleanup` on the old modules while their streams are still running. |
  | Outpost's WireGuard peer for the Edge | `persistent_keepalive_interval` | 25 s | It keeps the Outpost's NAT mapping open while no traffic flows, so the Edge can always reach it. |
  | Outpost byte copy | deadlines | only while reading the tnl header and dialling the Service; none while copying | Any idle deadline would cut a quiet stream. |

- **HTTP/3 needs no setting, and Caddy exposes none.** quic-go's idle timeout is 30 s. The 20 s heartbeat restarts it on both ends, so a stream survives even with a client that never sends a PING. A quiet gap longer than 30 s kills the connection unless the client PINGs; quic-go's HTTP/3 client PINGs every 10 s by default.
- **HTTP/2 uploads have a throughput ceiling, not a limit.** Go's HTTP/2 server allows 1 MiB in flight per stream and per connection, so one upload moves at most about 1 MiB per round trip. At 100 ms that is roughly 10 MiB/s, or 25 s for 250 MB. Caddy's JSON has no knob for it.
- **No layer limits request body size by default.** Caddy limits a body only when a `request_body` handler with `max_size` is configured.
- **netstack TCP never closes an idle connection, but it never notices a dead peer either.** Keepalive is off by default, and `wireguard-go`'s netstack API gives no way to turn it on. If an Outpost disappears without closing its connections, the Edge's side of each stream stays open until the visitor gives up. Fixing that is a "reconnects and keepalive" question for the map, not a requirement for v1 traffic.
- **For [#8 Zones and certificate modes](https://github.com/hyprsh/tnl/issues/8):** a Zone change made through `caddy.Load` keeps every open SSE stream on HTTP/1.1, HTTP/2 and HTTP/3, for as long as each stream lasts, provided `grace_period` stays 0. The price is that each reload leaves its old server instance alive until that instance's last stream ends, possibly for hours. WebSockets still close on every reload ([§9](#9-caddy-reloads)).

## Versions examined

| Component | Version | Source |
| --- | --- | --- |
| Caddy | v2.11.7 (latest release, 2026-10-03) | [github.com/caddyserver/caddy](https://github.com/caddyserver/caddy/tree/v2.11.7) |
| quic-go | v0.63.0 (latest release, 2026-09-22, and the version Caddy v2.11.7 requires) | [github.com/quic-go/quic-go](https://github.com/quic-go/quic-go/tree/v0.63.0), [Caddy `go.mod` L25](https://github.com/caddyserver/caddy/blob/v2.11.7/go.mod#L25) |
| Go | 1.27.2 (latest release) | [github.com/golang/go](https://github.com/golang/go/tree/go1.27.2) |
| golang.org/x/net | v0.59.0 (required by Caddy v2.11.7) | [github.com/golang/net](https://github.com/golang/net/tree/v0.59.0), [Caddy `go.mod` L48](https://github.com/caddyserver/caddy/blob/v2.11.7/go.mod#L48) |
| wireguard-go | `v0.0.0-20261006164505-2631ce99a06f` (`@latest`; no semver tags) | [WireGuard/wireguard-go@2631ce9](https://github.com/WireGuard/wireguard-go/tree/2631ce99a06f27120d581611cf125d68bc6aa565) |
| gVisor | `v0.0.0-20250503011706-39ed1f5ac29c`, the version `wireguard-go` pins | [google/gvisor@39ed1f5](https://github.com/google/gvisor/tree/39ed1f5ac29cb9a2d99d41502de53b8f0e2d19b6), [`wireguard-go` `go.mod` L10](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/go.mod#L10) |

With Go 1.27, `golang.org/x/net/http2` no longer has its own server: under the default build tags it wraps the HTTP/2 implementation inside `net/http` ([`server_wrap.go` L5](https://github.com/golang/net/blob/v0.59.0/http2/server_wrap.go#L5); [`net/http/http2.go` L22-L35](https://github.com/golang/go/blob/go1.27.2/src/net/http/http2.go#L22-L35)). The HTTP/2 facts below therefore cite `net/http/internal/http2` in Go 1.27.2. A build with Go 1.26 would use `x/net`'s older copy instead.

## The hops at a glance

| Hop | Default | Event streams for hours | 250 MB uploads | tnl sets |
| --- | --- | --- | --- | --- |
| Caddy server timeouts | read/write 0; read-header 1 min; idle 5 min; read-idle and write-idle 1 min | safe (v2.11.7+) | safe; HTTP/2 fails if the Service stalls > 1 min | keep defaults; pin ≥ v2.11.7 |
| Caddy body size | no limit | — | safe | nothing |
| `reverse_proxy` | SSE flushed immediately; bodies not buffered | safe | safe | nothing |
| `encode` | not in the chain unless configured | safe since v2.11.6 | — | leave it out |
| HTTP/2 | idle timer stopped while a stream is open; 1 MiB windows | safe | safe, about 1 MiB per round trip | nothing (no knob) |
| HTTP/3 | QUIC idle 30 s; HTTP/3 idle timer stopped while a request is open; 6 MiB stream window | safe with a 20 s heartbeat | safe | nothing (no knob) |
| tnl's `RoundTripper` | whatever tnl writes | safe if no `ResponseHeaderTimeout` shorter than the first event, no deadline | safe | see [§5](#5-tnls-roundtripper-over-netstack) |
| netstack TCP | no idle timeout; keepalive off; Nagle off; 1 MiB buffers, up to 4 MiB | safe | safe | optional dead-peer detection |
| WireGuard | no persistent keepalive; rekey every 2 min | safe while traffic flows | safe | Outpost keepalive 25 s |
| Outpost byte copy | whatever tnl writes | safe without copy deadlines | safe | header deadline only |
| Caddy reload | `grace_period` 0 (eternal) | survives on all three protocols | in-flight uploads finish | keep 0 |

## 1. Caddy's HTTP server

### 1.1 Defaults when embedded

- **The defaults are applied by the `http` app itself, so JSON loaded with `caddy.Load` gets them too.** During `Provision`, every server with a zero value gets `idle_timeout` 5 min, `read_header_timeout` 1 min, `read_idle_timeout` 1 min, `write_idle_timeout` 1 min and `max_write_chunk` 64 KiB ([`app.go` L401-L418](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L401-L418), [L920-L941](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L920-L941)). Neither the Caddyfile adapter nor the CLI is involved.
- **`read_timeout` and `write_timeout` default to 0, i.e. none.** They are passed straight to `http.Server.ReadTimeout` and `WriteTimeout` ([`app.go` L485-L490](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L485-L490)). Each also becomes a hard deadline anchored at the start of the request, capping the idle deadlines below ([`server.go` L576-L587](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/server.go#L576-L587)). Setting either would cut every stream and every upload that outlasts it.
- **TCP keepalive on visitor connections** is on by default, with 15 s idle, 15 s interval and 9 probes ([`server.go` L118-L137](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/server.go#L118-L137), [`app.go` L570-L577](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L570-L577)).
- **Request headers are limited to 16 KiB** since v2.11.6 ([`server.go` L139-L141](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/server.go#L139-L141); [v2.11.6 release notes](https://github.com/caddyserver/caddy/releases/tag/v2.11.6)). This limits headers only, not bodies.

### 1.2 Idle and header timeouts don't touch an active request

- **`idle_timeout`** is "the maximum time to wait for the next request when keep-alives are enabled" ([`server.go` L113-L116](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/server.go#L113-L116)). It is also passed to HTTP/2 and HTTP/3, which stop the timer while a stream is open ([§4](#4-http2-and-http3)).
- **`read_header_timeout`** bounds only the request headers ([`server.go` L66-L68](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/server.go#L66-L68)).

### 1.3 The new idle read and write timeouts (v2.11.6+)

- **How they work.** `ServeHTTP` wraps the request body in an `IdleTimeoutReader` and the response in an `IdleTimeoutWriter` ([`server.go` L571-L620](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/server.go#L571-L620)). Each sets a deadline of now + timeout right before every read or write, through `http.ResponseController` ([`idletimeout.go` L60-L72](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/idletimeout.go#L60-L72), [L100-L125](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/idletimeout.go#L100-L125), [L273-L295](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/idletimeout.go#L273-L295)). Large writes are split into 64 KiB chunks, so that a slow but progressing download isn't bounded by one deadline ([L184-L196](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/idletimeout.go#L184-L196)).
- **SSE: safe in v2.11.7.** The writer's doc says that with no minimum rate "the deadline only bounds the write or flush actually in flight, so a handler that pauses between writes (e.g. streaming or SSE) is unaffected." HTTP/2 is the exception, because there "the write deadline is a timer that resets the stream when it fires, even with no write in flight". Caddy therefore clears the deadline after every HTTP/2 write ([`idletimeout.go` L171-L214](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/idletimeout.go#L171-L214), [`server.go` L616](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/server.go#L616)). That fix is [#8119](https://github.com/caddyserver/caddy/pull/8119), released in v2.11.7. The read deadline is cleared when the body ends, so it no longer cuts a stream opened with a `POST` ([`idletimeout.go` L74-L79](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/idletimeout.go#L74-L79), [#8107](https://github.com/caddyserver/caddy/pull/8107)). The check confirmed both: streams with 3 s pauses under 2 s timeouts, by `GET` and by `POST`, on all three protocols.
- **What still fires on a stream.** A write that makes no progress for a minute does. Since an SSE event is tiny, that means the visitor's receive window has been full for a minute, i.e. the visitor has stopped reading. Cutting that stream is correct.
- **Uploads over HTTP/2: one real gap.** The reader sets its deadline before each read and clears it only when the body ends. Over HTTP/2, Go implements a read deadline as a timer that closes the request body when it fires, whether or not a read is in progress ([`internal/http2/server.go` L1845-L1853](https://github.com/golang/go/blob/go1.27.2/src/net/http/internal/http2/server.go#L1845-L1853), [L2716-L2740](https://github.com/golang/go/blob/go1.27.2/src/net/http/internal/http2/server.go#L2716-L2740)). Suppose the Service stops reading: `reverse_proxy` then blocks writing to it and stops reading the body. Once the buffers along the Tunnel are full, the timer fires a minute after the last read. The request fails with a 502 and an `i/o timeout` in Caddy's log, which the check reproduced. Over HTTP/1.1 the deadline only applies to a read in flight, and over HTTP/3 quic-go checks it only inside `Read` ([`receive_stream.go` L190-L215](https://github.com/quic-go/quic-go/blob/v0.63.0/receive_stream.go#L190-L215)). So uploads over those two protocols survived the same stall.
- **Tuning.** The server-wide fields are `read_idle_timeout`, `write_idle_timeout`, `read_min_rate` and `write_min_rate` ([`server.go` L70-L111](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/server.go#L70-L111)). A `timeouts` handler can set them per route ([`timeouts/timeouts.go`](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/timeouts/timeouts.go)), but tnl's Routes aren't Caddy routes, so for tnl this is effectively server-wide.
- **Related open issues worth tracking:**
  - [#8165](https://github.com/caddyserver/caddy/issues/8165), closed after v2.11.7: when a visitor stalls mid-upload, the HTTP/1.1 error response can hang.
  - [#8196](https://github.com/caddyserver/caddy/issues/8196), open: when the upstream answers early (for example 413) while an HTTP/1.1 body is still arriving, the visitor connection can desynchronise.

  Neither affects the normal path.

### 1.4 Request body size

- **No default limit.** Caddy limits a body only when a `request_body` handler sets `max_size`, which wraps the body in `http.MaxBytesReader` ([`requestbody.go` L35, L69-L70](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/requestbody/requestbody.go#L69-L70)).
- quic-go's `MaxHeaderBytes` "does not limit the size of the request body" ([`http3/server.go` L173-L177](https://github.com/quic-go/quic-go/blob/v0.63.0/http3/server.go#L173-L177)).
- The check pushed 250 MB through each protocol, at 63–85 MB/s on loopback.

### 1.5 Full duplex

`enable_full_duplex` (off by default) only matters when an HTTP/1.1 handler writes its response before it has read the whole body ([`server.go` L143-L161](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/server.go#L143-L161)). Neither SSE nor a normal upload does that, so tnl leaves it off.

## 2. `reverse_proxy`

- **SSE is flushed immediately, whatever `flush_interval` says.** `flushInterval` returns −1 ("immediately") in four cases ([`streaming.go` L269-L298](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/streaming.go#L269-L298)):
  - the upstream's `Content-Type` is `text/event-stream`;
  - the response has no `Content-Length`;
  - the response carries the new RFC 10036 `Incremental: ?1` header;
  - HTTP/2 is used on both sides.

  `copyResponse` then flushes after every write through `http.NewResponseController(dst).Flush` ([L317-L355](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/streaming.go#L317-L355)). The documented default is "no periodic flushing", which "is ignored when the upstream's response is recognized as a streaming response" ([`reverseproxy.go` L133-L140](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go#L133-L140)). In the check, events reached the visitor within 1–7 ms of the Service writing them.
- **Request bodies stream; they are buffered only on request.** `request_buffers` and `response_buffers` default to 0. A nonzero value reads the whole body into memory first ([`reverseproxy.go` L158-L173](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go#L158-L173), [L818-L838](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go#L818-L838)). A transport can also impose defaults by implementing `BufferedTransport` or `ContentLengthRequiredTransport` ([L304-L314](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go#L304-L314), [L1976-L1988](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go#L1976-L1988)). tnl's transport must implement neither.
- **Its own timeouts apply to upgraded connections only.** `stream_timeout` and `stream_close_delay` act on "streaming requests such as WebSockets", meaning hijacked connections, and are off by default ([`reverseproxy.go` L183-L200](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go#L183-L200)). SSE is an ordinary response, so neither applies.
- **The stock transport's timeouts don't apply,** because tnl brings its own `RoundTripper`. For reference, Caddy's `HTTPTransport` defaults are a 3 s dial timeout, a 2 min idle-connection timeout and 30 s TCP keepalive probes ([`httptransport.go` L207-L226](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/httptransport.go#L207-L226)). Its optional per-read and per-write `read_timeout`/`write_timeout` only wrap `*net.TCPConn`, so a netstack connection would skip them anyway ([L360-L370](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/httptransport.go#L360-L370)).

## 3. The `encode` handler

- **It no longer holds back SSE (v2.11.6+, [#7905](https://github.com/caddyserver/caddy/pull/7905)).** For a `text/event-stream` response, `WriteHeader` sets up the encoder and sends the headers immediately ([`encode.go` L296-L305](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/encode/encode.go#L296-L305)). Every `Flush` flushes the compressor first ([L332-L362](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/encode/encode.go#L332-L362)). In the check, events behind `encode gzip` arrived gzip-encoded within 7 ms on all three protocols.
- **It does compress SSE.** The default matcher includes `text/*` ([L86-L130](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/encode/encode.go#L86-L130)), The 512-byte minimum length ([L654-L655](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/encode/encode.go#L654-L655)) never applies to SSE, because it is checked only before the headers have gone out ([L386-L400](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/encode/encode.go#L386-L400)).
- **tnl doesn't need it.** `encode` is only in the chain if tnl's config puts it there. Leaving it out keeps the Edge byte-transparent and lets Services do their own compression.

## 4. HTTP/2 and HTTP/3

### 4.1 HTTP/2 (Go 1.27.2 `net/http`)

- **Caddy configures HTTP/2 with an empty `http2.Server`** ([`app.go` L520-L533](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L520-L533)). The HTTP/2 idle timeout is taken from the `http.Server`'s `IdleTimeout` ([`server_wrap.go` L42-L48](https://github.com/golang/net/blob/v0.59.0/http2/server_wrap.go#L42-L48)).
- **The idle timer only runs while no stream is open.** A new stream stops it ([`internal/http2/server.go` L1906-L1908](https://github.com/golang/go/blob/go1.27.2/src/net/http/internal/http2/server.go#L1906-L1908)), and it is reset only when the last stream closes ([L1590-L1595](https://github.com/golang/go/blob/go1.27.2/src/net/http/internal/http2/server.go#L1590-L1595)). An open SSE stream keeps the connection out of it.
- **No server PINGs by default.** `SendPingTimeout` is zero unless configured ([L809-L813](https://github.com/golang/go/blob/go1.27.2/src/net/http/internal/http2/server.go#L809-L813)), and `write_timeout` 0 means no per-stream write timer.
- **Flow control: 1 MiB per stream and per connection** on the server's receive side ([`internal/http2/config.go` L50-L66](https://github.com/golang/go/blob/go1.27.2/src/net/http/internal/http2/config.go#L50-L66)). Caddy sets no `HTTP2Config`, so uploads get these defaults. They cap throughput per round trip without blocking anything. A 20 s heartbeat is tiny compared with the visitor's own window.

### 4.2 HTTP/3 (quic-go v0.63.0)

- **QUIC idle timeout 30 s, no keepalive PINGs from the server.**
  - Caddy builds its QUIC listener with only `InitialPacketSize`, `Allow0RTT` and a tracer set ([`listeners.go` L472-L487](https://github.com/caddyserver/caddy/blob/v2.11.7/listeners.go#L472-L487)), so quic-go's defaults apply: `MaxIdleTimeout` 30 s and `KeepAlivePeriod` 0 ([`params.go` L93-L97](https://github.com/quic-go/quic-go/blob/v0.63.0/internal/protocol/params.go#L93-L97); [`config.go` L71-L74, L108-L113](https://github.com/quic-go/quic-go/blob/v0.63.0/config.go#L71-L113)).
  - The Caddy code notes that `http3.Server.QUICConfig` is ignored because Caddy brings its own listener ([`server.go` L1063-L1075](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/server.go#L1063-L1075)), and Caddy's JSON exposes no QUIC idle setting.
- **Why a 20 s heartbeat is enough.** The effective idle timeout is the smaller of the two endpoints' values ([RFC 9000 §10.1](https://www.rfc-editor.org/rfc/rfc9000.html#section-10.1); [`connection.go` L2417-L2425](https://github.com/quic-go/quic-go/blob/v0.63.0/connection.go#L2417-L2425)). The timer restarts on every packet received, or on the first ack-eliciting packet sent after one was received ([L846-L849](https://github.com/quic-go/quic-go/blob/v0.63.0/connection.go#L846-L849), [L905-L911](https://github.com/quic-go/quic-go/blob/v0.63.0/connection.go#L905-L911)). Each heartbeat restarts the client's timer when it arrives and the server's when the client's ACK comes back. Chromium's default idle timeout is also 30 s, or 300 s under a field trial ([`quic_context.h` L67-L75](https://github.com/chromium/chromium/blob/e7f9bd0d548b30192be3950a871fbb12db096f27/net/quic/quic_context.h#L67-L75)). In the check, a stream with 20 s gaps survived 60 s even with client PINGs switched off. A single 35 s gap killed the connection at 30 s ("no recent network activity"). With quic-go's default client, which PINGs every 10 s ([`http3/client.go` L45-L48](https://github.com/quic-go/quic-go/blob/v0.63.0/http3/client.go#L45-L48)), the same gap survived. The margin is 10 s. A Service whose heartbeat drifts towards 30 s would lose HTTP/3 visitors whose browser doesn't PING. Whether browsers PING on an open stream wasn't checked here.
- **The HTTP/3 idle timer is stopped while a request is open.** Caddy passes `idle_timeout` (5 min) as `http3.Server.IdleTimeout`, which "refers only to the HTTP/3 layer" ([`http3/server.go` L183-L187](https://github.com/quic-go/quic-go/blob/v0.63.0/http3/server.go#L183-L187)). Its timer is stopped when a request stream starts and reset only when the last stream closes ([`http3/server_conn.go` L59-L71, L100-L105](https://github.com/quic-go/quic-go/blob/v0.63.0/http3/server_conn.go#L59-L105)).
- **Flow control:** the stream window starts at 512 KiB and auto-tunes up to 6 MiB; the connection window goes up to 15 MiB ([`params.go` L18-L34](https://github.com/quic-go/quic-go/blob/v0.63.0/internal/protocol/params.go#L18-L34)). That is enough for 250 MB, as the check showed.
- **Path MTU:** Caddy v2.11.6 lowered the initial QUIC packet size to 1200 bytes for low-MTU links ([v2.11.6 release notes](https://github.com/caddyserver/caddy/releases/tag/v2.11.6)). An open issue reports HTTP/3 stalls when PMTU discovery overshoots ([#8175](https://github.com/caddyserver/caddy/issues/8175)). This sits on the visitor's side of the Edge, not in the Tunnel.

## 5. tnl's `RoundTripper` over netstack

[Data path](https://github.com/hyprsh/tnl/issues/5) decided that tnl writes its own `http.RoundTripper` instead of relying on Caddy's undocumented transport field. Built on Go's `http.Transport`, this is what each field does to the two traffic kinds:

- **`ResponseHeaderTimeout`.** The timer starts only "after fully writing the request (including its body, if any)" and stops once the headers arrive ([`transport.go` L231-L235](https://github.com/golang/go/blob/go1.27.2/src/net/http/transport.go#L231-L235), [L3093-L3108](https://github.com/golang/go/blob/go1.27.2/src/net/http/transport.go#L3093-L3108)). A 250 MB upload can't trip it while sending, and a running stream can't trip it at all. One SSE case can: a Service that sends no headers until its first event, with the first heartbeat 20 s away. So leave it unset, or set it well above any first-event delay (minutes).
- **`IdleConnTimeout`** only closes pooled connections that sit idle; zero means no limit ([L225-L229](https://github.com/golang/go/blob/go1.27.2/src/net/http/transport.go#L225-L229)). Around 90 s, the value of Go's `DefaultTransport`, releases unused Tunnel connections and their Service connections.
- **`ExpectContinueTimeout` 0** makes the body go out at once, "without waiting for the server to approve" ([L237-L244](https://github.com/golang/go/blob/go1.27.2/src/net/http/transport.go#L237-L244)).
- **`DisableCompression: true`.** Otherwise the transport adds `Accept-Encoding: gzip` to requests that have none and transparently decompresses the reply ([L199-L207](https://github.com/golang/go/blob/go1.27.2/src/net/http/transport.go#L199-L207)). That changes bytes, not timing, but an Edge that only relays shouldn't do it.
- **HTTP/1.1 only towards the Service** (`Protocols` with only HTTP/1 set).
  - Then each request is one Tunnel connection and one Service connection, and the Outpost's copy stays byte-for-byte.
  - HTTP/2 towards an `https://` Service would add a second set of flow-control windows and PING rules for nothing.
  - Go only tries HTTP/2 with a custom dialer when `ForceAttemptHTTP2` is set ([L301-L306](https://github.com/golang/go/blob/go1.27.2/src/net/http/transport.go#L301-L306)).
- **Dialling.** `wireguard-go`'s `DialContext` honours the context ([`tun/netstack/tun.go` L208-L216](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/tun/netstack/tun.go#L208-L216)). tnl should bound the dial plus the tnl header reply with a short context timeout, but never set a deadline on the connection once it is handed to `http.Transport`. `reverse_proxy` passes the visitor's request context, so a visitor who leaves cancels the round trip and closes the Tunnel connection.
- **No `http.Client`.** `reverse_proxy` calls `RoundTrip` directly, so there is no `Client.Timeout` to forget.
- **`Cleanup` must only drop idle connections.** A reload cleans up the old config's modules while their requests are still streaming ([§9](#9-caddy-reloads)). Calling `CloseIdleConnections` there is safe, which is what Caddy's own transport does ([`httptransport.go` L714-L725](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/httptransport.go#L714-L725)). Tearing down the netstack or closing active connections would cut every stream.

## 6. gVisor netstack TCP on the Edge and the Outpost

- **No idle timeout.** An established gVisor TCP endpoint is closed only by:
  - the application;
  - a reset from the peer;
  - retransmission giving up: 15 retries with the RTO doubling up to 120 s, about 15 minutes of unacknowledged data ([`snd.go` L31-L51](https://github.com/google/gvisor/blob/39ed1f5ac29cb9a2d99d41502de53b8f0e2d19b6/pkg/tcpip/transport/tcp/snd.go#L31-L51), [L557-L597](https://github.com/google/gvisor/blob/39ed1f5ac29cb9a2d99d41502de53b8f0e2d19b6/pkg/tcpip/transport/tcp/snd.go#L557-L597), [L657-L663](https://github.com/google/gvisor/blob/39ed1f5ac29cb9a2d99d41502de53b8f0e2d19b6/pkg/tcpip/transport/tcp/snd.go#L657-L663));
  - keepalive, if enabled.

  A quiet connection can therefore live indefinitely.
- **Keepalive is off by default, and `wireguard-go` gives no way to turn it on.**
  - The endpoint defaults are 2 h idle, 75 s interval and 9 probes, used only when keepalive is enabled per endpoint ([`protocol.go` L71-L81](https://github.com/google/gvisor/blob/39ed1f5ac29cb9a2d99d41502de53b8f0e2d19b6/pkg/tcpip/transport/tcp/protocol.go#L71-L81), [`endpoint.go` L497-L507, L862-L866](https://github.com/google/gvisor/blob/39ed1f5ac29cb9a2d99d41502de53b8f0e2d19b6/pkg/tcpip/transport/tcp/endpoint.go#L497-L507)).
  - `wireguard-go`'s netstack sets only SACK on its stack ([`tun/netstack/tun.go` L55-L74](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/tun/netstack/tun.go#L55-L74)) and keeps the stack in an unexported field ([L42-L53](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/tun/netstack/tun.go#L42-L53)).
  - Its dial and listen helpers return `gonet.TCPConn`, whose endpoint is unexported and which has no keepalive method ([`gonet.go` L229-L246](https://github.com/google/gvisor/blob/39ed1f5ac29cb9a2d99d41502de53b8f0e2d19b6/pkg/tcpip/adapters/gonet/gonet.go#L229-L246)).
- **Why it matters little for v1 traffic.** During an SSE stream the Edge only receives. If an Outpost vanishes without a FIN or RST (power loss, network gone), the Edge's side of each of its streams stays open, but it is not leaked forever:
  - The visitor sees no more heartbeats. An SSE client that reconnects on a missing heartbeat cancels the request, which closes the Tunnel connection.
  - A visitor who has also vanished is noticed by Caddy, at the latest after about 15 min of unacknowledged writes ([Linux `tcp_retries2`](https://docs.kernel.org/networking/ip-sysctl.html)) or 30 s of QUIC silence.

  In the other direction the Outpost sends heartbeats, so a vanished Edge is noticed by retransmission (~15 min). A restarted Edge has a fresh netstack. Once WireGuard has re-handshaked, it answers the Outpost's next segment for an unknown connection with a reset ([`protocol.go` L166-L181](https://github.com/google/gvisor/blob/39ed1f5ac29cb9a2d99d41502de53b8f0e2d19b6/pkg/tcpip/transport/tcp/protocol.go#L166-L181)).
- **If tnl wants prompt dead-peer detection** (a question for the map's "reconnects, keepalive" item), it has two options:
  1. Build its own netstack device, roughly the first 200 MIT-licensed lines of `wireguard-go`'s `tun/netstack`, so it can enable keepalive and set idle/interval on each endpoint.
  2. Close an Outpost's Tunnel connections when its last WireGuard handshake grows stale. With a 25 s persistent keepalive, packets never stop. Since keys are rekeyed after 120 s and rejected after 180 s, a live Outpost's handshake is never more than about 3 minutes old ([`device/send.go` L195-L204](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/send.go#L195-L204), [§7](#7-wireguard)).
- **No buffering of small writes.** Nagle's algorithm is off unless the stack sets `TCPDelayEnabled`, which `wireguard-go` doesn't ([`endpoint.go` L902-L905](https://github.com/google/gvisor/blob/39ed1f5ac29cb9a2d99d41502de53b8f0e2d19b6/pkg/tcpip/transport/tcp/endpoint.go#L902-L905), [`protocol.go` L572-L599](https://github.com/google/gvisor/blob/39ed1f5ac29cb9a2d99d41502de53b8f0e2d19b6/pkg/tcpip/transport/tcp/protocol.go#L572-L599)). A `gonet.TCPConn.Read` returns whatever has arrived without waiting to fill the buffer ([`gonet.go` L302-L357](https://github.com/google/gvisor/blob/39ed1f5ac29cb9a2d99d41502de53b8f0e2d19b6/pkg/tcpip/adapters/gonet/gonet.go#L302-L357)).
- **Buffers are bounded, so backpressure works.** Send and receive buffers default to 1 MiB, and receive-buffer auto-tuning grows them up to 4 MiB ([`protocol.go` L44-L53, L572-L587](https://github.com/google/gvisor/blob/39ed1f5ac29cb9a2d99d41502de53b8f0e2d19b6/pkg/tcpip/transport/tcp/protocol.go#L44-L53)). A 250 MB upload holds a few MiB per connection, not the whole body.

## 7. WireGuard

- **The Outpost needs `persistent_keepalive_interval = 25`.**
  - `wireguard-go` sends a keepalive only after that many seconds without any authenticated packet in either direction, and the default is off ([`device/timers.go` L131-L135, L193-L199](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/timers.go#L131-L199); [`device/uapi.go` L350](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/uapi.go#L350)).
  - WireGuard's docs recommend it for a peer behind NAT that "wants to receive incoming connections long after network traffic has gone silent". They call 25 s "a sensible interval that works with a wide variety of firewalls" ([WireGuard quickstart](https://www.wireguard.com/quickstart/#nat-and-firewall-traversal-persistence)).
  - NATs must keep a UDP mapping at least 2 minutes and should keep it 5, refreshed by outbound traffic ([RFC 4787 §4.3 REQ-5, REQ-6](https://www.rfc-editor.org/rfc/rfc4787#section-4.3)). Real devices may keep it shorter. Linux's conntrack defaults, for example, drop an unreplied UDP flow after 30 s and a two-way one after 120 s ([nf_conntrack sysctl](https://docs.kernel.org/networking/nf_conntrack-sysctl.html)).
- **What it is for.** An active SSE stream refreshes the mapping by itself, since a heartbeat leaves the Outpost every 20 s. The keepalive matters between streams: the Edge opens every Tunnel connection, so it must be able to reach an idle Outpost at any time. The Edge has a public address and needs no keepalive of its own. An Outpost that roams is found again from its next authenticated packet ([WireGuard, Built-in Roaming](https://www.wireguard.com/#built-in-roaming)).
- **Rekeying is invisible to TCP.** Keys rotate every 120 s and are rejected after 180 s. A peer that receives data and has nothing to send replies with a keepalive after 10 s ([`device/constants.go` L15-L23](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/constants.go#L15-L23), [`timers.go` L145-L153](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/timers.go#L145-L153)). A connection that lives for hours goes through many handshakes, which the TCP inside never sees.
- **MTU.** `wireguard-go`'s default MTU is 1420 ([`device/tun.go` L14](https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/tun.go#L14)). It isn't a timeout, but it is the one thing in the Tunnel that could stall an upload and not a heartbeat. Uploads use full-size segments, so if the outer path can't carry 1420 + 80 bytes, a 250 MB upload stalls while small events still pass.

## 8. The Outpost's byte copy

- **Deadlines belong only before the copy:**
  - a read deadline while reading the tnl header, then cleared;
  - a dial timeout towards the Service;
  - none while copying.

  An SSE stream may be quiet for 20 s and other Services for much longer, so any idle deadline in the copy would cut legitimate traffic. The check's Outpost works this way: a 10 s deadline on the header and a 5 s dial timeout.
- **The Service socket gets Go's defaults:**
  - TCP keepalive with 15 s idle, 15 s interval and 9 probes, unless disabled ([`net/dial.go` L18-L27, L175-L185](https://github.com/golang/go/blob/go1.27.2/src/net/dial.go#L175-L185));
  - no Nagle delay: the default "is true (no delay), meaning that data is sent as soon as possible after a Write" ([`net/tcpsock.go` L259-L263](https://github.com/golang/go/blob/go1.27.2/src/net/tcpsock.go#L259-L263)).

  So an event the Service writes is forwarded as soon as it arrives.
- **Half-close in both directions.** When one side reaches EOF, `CloseWrite` the other and keep copying the opposite direction until it ends too. `gonet.TCPConn` and `net.TCPConn` both support it ([`gonet.go` L437](https://github.com/google/gvisor/blob/39ed1f5ac29cb9a2d99d41502de53b8f0e2d19b6/pkg/tcpip/adapters/gonet/gonet.go#L437)). Since the Outpost never parses HTTP, this is all it needs to know about request and response boundaries.

## 9. Caddy reloads

### 9.1 What a reload does to an open stream

- **The old servers are shut down gracefully, in the background.**
  1. `caddy.Load` starts the new config, then calls `Stop` on the old apps.
  2. The `http` app calls `http.Server.Shutdown(ctx)` and `http3.Server.Shutdown(ctx)` in goroutines and returns once they have started, without waiting ([`app.go` L753-L875](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L753-L875)).
  3. With `grace_period` 0, `ctx` is `context.Background()`, which "is eternal" ([L136-L142](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L136-L142)).
- **HTTP/1.1.** `Shutdown` closes the listeners and the idle connections, then waits "indefinitely for connections to return to idle" ([`net/http/server.go` L3252-L3311](https://github.com/golang/go/blob/go1.27.2/src/net/http/server.go#L3252-L3311), [L3326-L3348](https://github.com/golang/go/blob/go1.27.2/src/net/http/server.go#L3326-L3348)). A connection carrying an SSE response is active, so it is left alone until the response ends.
- **HTTP/2.** `net/http` marks an HTTP/2 connection active for its whole life, so `closeIdleConns` never closes it ([`server.go` L2016-L2026](https://github.com/golang/go/blob/go1.27.2/src/net/http/server.go#L2016-L2026)). Shutdown sends a graceful GOAWAY, and the connection "isn't closed until all current streams are done" ([`internal/http2/server.go` L1317-L1327](https://github.com/golang/go/blob/go1.27.2/src/net/http/internal/http2/server.go#L1317-L1327), [L895-L902](https://github.com/golang/go/blob/go1.27.2/src/net/http/internal/http2/server.go#L895-L902)). The browser opens a new connection for its next request ([RFC 9113 §6.8](https://www.rfc-editor.org/rfc/rfc9113.html#section-6.8)).
- **HTTP/3.**
  - Caddy shares one QUIC listener per address across configs, so existing QUIC connections stay on the same socket after a reload ([`listeners.go` L434-L508](https://github.com/caddyserver/caddy/blob/v2.11.7/listeners.go#L434-L508)).
  - The old `http3.Server` sends GOAWAY and waits for its requests to finish, or "until the context is cancelled", and then closes every connection ([`http3/server.go` L564-L579, L636-L680](https://github.com/quic-go/quic-go/blob/v0.63.0/http3/server.go#L636-L680)). With an eternal grace period that never happens.
- **Measured.** In the check, SSE streams on all three protocols carried on through two reloads with no gap. The [Can one Go binary embed Caddy and userspace WireGuard?](https://github.com/hyprsh/tnl/issues/3) spike had shown the same for one HTTP/1.1 stream.

### 9.2 What limits how long it survives

- **With `grace_period` 0: nothing but the stream itself.** No timer is armed on the old server, its connections or its QUIC listener. A stream that lasts hours lasts hours, as long as the timeouts in [§1](#1-caddys-http-server) stay as recommended.
- **With a finite `grace_period`, HTTP/3 streams are cut when it ends, and HTTP/1.1 and HTTP/2 streams are not yet:**
  - v2.11.7 now keeps the grace context alive until the shutdowns finish ([#8009](https://github.com/caddyserver/caddy/pull/8009), [`app.go` L755-L766](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L755-L766)).
  - When it expires, quic-go closes every remaining HTTP/3 connection ([`http3/server.go` L672-L679](https://github.com/quic-go/quic-go/blob/v0.63.0/http3/server.go#L672-L679)).
  - `net/http`'s `Shutdown` merely returns the context's error ([`server.go` L3304-L3306](https://github.com/golang/go/blob/go1.27.2/src/net/http/server.go#L3304-L3306)), and Caddy never calls `Close`. So HTTP/1.1 and HTTP/2 connections outlive the grace period, contrary to the doc comment "Once the grace period is over, connections will be forcefully closed" ([`app.go` L136-L142](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L136-L142)).
  - The check showed exactly this. With a 3 s grace period, the HTTP/3 stream ended 3 s after the reload with `H3_NO_ERROR`; the HTTP/1.1 and HTTP/2 streams ran to completion. Caddy logged `server shutdown … server graceful shutdown 3s timeout`.
  - The open issue [#8078](https://github.com/caddyserver/caddy/issues/8078) plans to add the missing `Close()`. After that, a finite grace period will cut every protocol.
- **`shutdown_delay`** only postpones the start of shutdown for listeners that are closing for good, not for listeners shared with the new config ([`app.go` L725-L751](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L725-L751)).
- **Process exit cuts everything.** An embedding's `caddy.Stop()` doesn't mark the process as exiting, so the `http` app doesn't wait for anything ([`caddy.go` L694-L710](https://github.com/caddyserver/caddy/blob/v2.11.7/caddy.go#L694-L710), [L839-L843](https://github.com/caddyserver/caddy/blob/v2.11.7/caddy.go#L839-L843); [`app.go` L855-L875](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L855-L875)). When tnl's process ends, every stream ends, which the map already notes.

### 9.3 What a reload costs

- **Old server instances live as long as their streams.** With an eternal grace period, each reload leaves one generation of `http.Server`, `http3.Server`, handlers and TLS connection policies running until its last stream closes. Caddy's own comment warns that "frequent config reloads with long grace periods for a sustained length of time may deplete resources" ([`app.go` L855-L861](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/app.go#L855-L861)). With Zone changes as a rare operator action, that's a handful of generations at most.
- **Modules are cleaned up while their requests still run.** `unsyncedStop` cancels the old config's context right after `Stop` returns, and that calls `Cleanup` on every module of the old config ([`caddy.go` L722-L740](https://github.com/caddyserver/caddy/blob/v2.11.7/caddy.go#L722-L740), [`context.go` L72-L96](https://github.com/caddyserver/caddy/blob/v2.11.7/context.go#L72-L96)). Upstream tracks this in the open [#8143](https://github.com/caddyserver/caddy/issues/8143).
  - `reverse_proxy`'s `Cleanup` closes only hijacked (upgraded) connections and deletes its static upstreams from the host pool ([`reverseproxy.go` L460-L469](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go#L460-L469), [`streaming.go` L460-L490](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/streaming.go#L460-L490)). SSE responses are untouched.
  - tnl's Route-lookup module and transport must follow the same rule: shared state (netstack, Route table) lives outside Caddy, and `Cleanup` releases only what no running request uses.
- **WebSockets** (lasting guarantees are deferred to v2) are closed by that same `Cleanup` on every reload, unless `stream_close_delay` postpones it.

## 10. Throwaway check

Committed next to this file as [`visitor-to-service-timeouts/check/main.go`](visitor-to-service-timeouts/check/main.go). It is research code, not tnl code. It used Go 1.27.2, Caddy v2.11.7, quic-go v0.63.0 and `wireguard-go` `2631ce9`, and ran on an Apple M1 under macOS as uid 501 (not root).

**Setup, all in one process on loopback**

- **Service:** plain HTTP on `127.0.0.1`.
  - `/sse?gaps=…` writes an event, waits each gap, writes the next, and flushes after each.
  - `/upload` counts body bytes, optionally stopping for a while after N bytes.
- **Outpost:** a `wireguard-go` netstack at `100.64.0.2` with `persistent_keepalive_interval=25`.
  - It accepts Tunnel connections on port 7000 and reads `TNL1 host:port\n` under a 10 s deadline.
  - It dials the Service with a 5 s timeout, replies `OK`, then copies both ways with half-close and no deadlines.
- **Edge:** a netstack at `100.64.0.1`, plus embedded Caddy loaded with `caddy.Load`.
  - TLS comes from Caddy's internal issuer for `app.test` and `enc.test`. `enc.test` puts `encode gzip` in front of `reverse_proxy`.
  - `reverse_proxy` uses a ~50-line `http.reverse_proxy.transport.tnlcheck` module. It is an `http.Transport` that dials the Outpost through the netstack, sends the header and waits for `OK`, with HTTP/1.1 only, `DisableCompression`, no response-header timeout, and `Cleanup` closing idle connections only.
  - Server timeouts were shrunk to 2 s: `read_header_timeout`, `idle_timeout`, `read_idle_timeout` and `write_idle_timeout`.
- **Clients:** Go's HTTP/1.1 and HTTP/2 transports, and quic-go's HTTP/3 transport, a fresh connection per test. Latency is the time from the Service's write to the client reading the event.

**Observed**

```
1. SSE with 3 s pauses; read_idle, write_idle and idle timeouts are 2 s
   GET, 4 gaps of 3s                  HTTP/1.1 events  5/5   max latency 1ms        lasted 12s     enc=     end: EOF
   POST with 1 KiB body, 4 gaps of 3s HTTP/1.1 events  5/5   max latency 1.5ms      lasted 12s     enc=     end: EOF
   GET through encode, 4 gaps of 3s   HTTP/1.1 events  5/5   max latency 6.8ms      lasted 12s     enc=gzip end: EOF
   GET, 4 gaps of 3s                  HTTP/2.0 events  5/5   max latency 900µs      lasted 12s     enc=     end: EOF
   POST with 1 KiB body, 4 gaps of 3s HTTP/2.0 events  5/5   max latency 5.8ms      lasted 12s     enc=     end: EOF
   GET through encode, 4 gaps of 3s   HTTP/2.0 events  5/5   max latency 1.1ms      lasted 12s     enc=gzip end: EOF
   GET, 4 gaps of 3s                  HTTP/3.0 events  5/5   max latency 1.4ms      lasted 12s     enc=     end: EOF
   POST with 1 KiB body, 4 gaps of 3s HTTP/3.0 events  5/5   max latency 1.3ms      lasted 12s     enc=     end: EOF
   GET through encode, 4 gaps of 3s   HTTP/3.0 events  5/5   max latency 6.5ms      lasted 12s     enc=gzip end: EOF

2. 250 MB uploads, one at a time
   250 MB                             h1  status 200 in 3.4s   (74 MB/s)  service saw: {"bytes":250000000,"err":""}
   250 MB                             h2  status 200 in 2.9s   (85 MB/s)  service saw: {"bytes":250000000,"err":""}
   250 MB                             h3  status 200 in 4s     (63 MB/s)  service saw: {"bytes":250000000,"err":""}

3. 64 MB upload, Service stops reading for 4 s after 8 MB (read_idle 2 s)
   64 MB, 4 s stall                   h1  status 200 in 4.8s   (13 MB/s)  service saw: {"bytes":64000000,"err":""}
   64 MB, 4 s stall                   h2  status 502 in 4.1s   (16 MB/s)  service saw:
   64 MB, 4 s stall                   h3  status 200 in 5.1s   (12 MB/s)  service saw: {"bytes":64000000,"err":""}

4. SSE (1 event/s for 10 s) across two reloads at t=3s and t=6s, grace_period 0 (eternal)
   GET, reloads, eternal grace        HTTP/1.1 events 11/11  max latency 1.4ms      lasted 10s     enc=     end: EOF
   GET, reloads, eternal grace        HTTP/2.0 events 11/11  max latency 1.5ms      lasted 10s     enc=     end: EOF
   GET, reloads, eternal grace        HTTP/3.0 events 11/11  max latency 1.6ms      lasted 10s     enc=     end: EOF

5. SSE (1 event/s for 12 s) across one reload at t=2s; the old config has grace_period 3s
   GET, reload, grace 3s              HTTP/1.1 events 13/13  max latency 1.5ms      lasted 12s     enc=     end: EOF
   GET, reload, grace 3s              HTTP/2.0 events 13/13  max latency 1.6ms      lasted 12s     enc=     end: EOF
   GET, reload, grace 3s              HTTP/3.0 events  5/13  max latency 1ms        lasted 5s      enc=     end: H3_NO_ERROR

6. QUIC idle timeout (30 s): 20 s heartbeats vs. one 35 s pause (* = client PINGs off)
   GET, 3 gaps of 20s                 HTTP/1.1 events  4/4   max latency 1.5ms      lasted 1m0s    enc=     end: EOF
   GET, 1 gap of 35s                  HTTP/1.1 events  2/2   max latency 2.4ms      lasted 35s     enc=     end: EOF
   GET, 3 gaps of 20s                 HTTP/2.0 events  4/4   max latency 3ms        lasted 1m0s    enc=     end: EOF
   GET, 1 gap of 35s                  HTTP/2.0 events  2/2   max latency 2.8ms      lasted 35s     enc=     end: EOF
   GET, 3 gaps of 20s                 HTTP/3.0 events  4/4   max latency 2.8ms      lasted 1m0s    enc=     end: EOF
   GET, 1 gap of 35s                  HTTP/3.0 events  2/2   max latency 2.4ms      lasted 35s     enc=     end: EOF
   GET, 3 gaps of 20s                 HTTP/3.0* events  4/4   max latency 3ms        lasted 1m0s    enc=     end: EOF
   GET, 1 gap of 35s                  HTTP/3.0* events  1/2   max latency 600µs      lasted 30.1s   enc=     end: timeout: no recent network activity
```

- In step 3, Caddy logged the HTTP/2 failure as `i/o timeout` on the `POST /upload`.
- In step 5, Caddy logged `servers shutting down; grace period initiated` at the reload, and `server shutdown` / `HTTP/3 server shutdown … server graceful shutdown 3s timeout` 3 s later. The HTTP/1.1 and HTTP/2 connections stayed open after that.
- Throughput is loopback through two userspace stacks on one laptop. It is an indication, not a VPS benchmark.

**Not checked**

- Streams lasting hours. The timeouts were shrunk instead, and the source shows no timer that depends on a stream's age.
- Real browsers, real NATs and a Linux VPS.

## 11. Questions this raises

These go to other tickets; this report doesn't decide them.

- **[#8 Zones and certificate modes](https://github.com/hyprsh/tnl/issues/8).** Zones can live in Caddy's config without cutting SSE streams, but only if tnl keeps `grace_period` at 0. The open questions are whether the occasional old server generation living for hours is acceptable, and whether WebSockets closing on each Zone change can wait for v2.
- **Map, "Reconnects, keepalive …".**
  - The Outpost's 25 s persistent keepalive is settled here.
  - Still open: does the Edge need prompt dead-Outpost detection on open streams? That would take either tnl's own netstack device (TCP keepalive per endpoint) or closing connections on a stale WireGuard handshake ([§6](#6-gvisor-netstack-tcp-on-the-edge-and-the-outpost)).
- **Attachments (milestone 5).** Should `read_idle_timeout` be raised above 1 minute, in case a Service pauses reading an upload ([§1.3](#13-the-new-idle-read-and-write-timeouts-v2116))? And is HTTP/2's 1 MiB-per-round-trip upload ceiling acceptable on long round trips?
- **Upgrades.** Caddy's timeout and reload behaviour changed twice in two weeks (v2.11.6, v2.11.7), and [#8078](https://github.com/caddyserver/caddy/issues/8078) will change it again. tnl's tests should replay [§10](#10-throwaway-check) on every Caddy bump.
