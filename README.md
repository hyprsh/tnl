# tnl

tnl puts a Service on your home or office network at a public `https://` address. The Edge, a small server with a public IP, terminates HTTPS. An Outpost, running on the network where the Service lives, joins the Edge over userspace WireGuard and forwards the traffic. Neither side needs root, a TUN device or a firewall hole at the Outpost.

The design is in [`docs/spec/v1.md`](docs/spec/v1.md), the terms in [`GLOSSARY.md`](GLOSSARY.md).

> This is the from-source quick start for the tracer bullet: one `http://` Service per Route, a certificate for each Hostname, no packages yet. The install script and the systemd units come with the first release.

## Quick start

You need two machines, or two terminals on one for a trial:

- **The Edge host** has a public IP and a DNS name, here `edge.example.com`.
- **The Outpost host** is on the network where the Service runs. Here the Service is `localhost:3000` on that host, and the Outpost is called `nas`.

### 1. Build

You need Go 1.27 or newer. There is no cgo and no xcaddy.

```sh
git clone https://github.com/hyprsh/tnl && cd tnl
go build -tags nomysql -o tnl ./cmd/tnl
```

Copy `tnl` to both hosts. For the Outpost alone, `go build -tags nomysql,outpost` makes a smaller binary that leaves out the Edge and Caddy.

### 2. Open the ports

On the Edge host's firewall and your cloud provider's security group:

| Port | Protocol | For |
|---|---|---|
| 80 | TCP | ACME HTTP-01 challenges, and a redirect to HTTPS |
| 443 | TCP | HTTPS, HTTP/1.1 and HTTP/2 |
| 443 | UDP | HTTP/3 |
| 51820 | UDP | WireGuard, which Outposts dial |

The Outpost host needs no inbound port. It only has to reach the Edge's UDP port 51820.

Point the DNS names at the Edge host: `edge.example.com` for the Outposts, and `app.example.com` (or a wildcard record) for the Hostnames you will serve.

### 3. Run the Edge as a plain user

The Edge needs one capability, to bind ports 80 and 443, and nothing else. Give the binary that capability and run it as an ordinary user:

```sh
sudo setcap cap_net_bind_service=+ep ./tnl
mkdir -p ~/tnl-edge
./tnl edge --address edge.example.com --state-dir ~/tnl-edge --acme-email you@example.com
```

`--address` is the DNS name Outposts dial. Certificates come from Let's Encrypt production unless you pass `--acme-ca` (or `TNL_ACME_CA`) with another ACME directory URL, such as Let's Encrypt staging while you try things out. Every flag also has a `TNL_*` variable; see `tnl edge -h`.

The Edge keeps everything in its state directory, in `state.json`, and builds Caddy's config from it at every start. Routes survive a restart.

### 4. Add a Zone and an Outpost

In another terminal on the Edge host, with the same `--state-dir`:

```sh
./tnl zone add example.com --state-dir ~/tnl-edge
./tnl outpost add nas --state-dir ~/tnl-edge
```

`zone add` says which DNS zone the Edge may serve Hostnames in. `outpost add` prints a join token. The token is shown once and is never stored. It holds the key the Outpost joins with, so send it over a channel you trust.

(`--state-dir` can also be set once with `TNL_STATE_DIR`.)

### 5. Connect the Outpost

On the Outpost host, as a plain user:

```sh
./tnl connect --token <the token>
```

This brings the Tunnel up and keeps running. Leave it in a terminal, or run it under your own supervisor. The Outpost saves what it needs in its state directory, so later runs need no token.

### 6. Add a Route

On the Edge host:

```sh
./tnl route add app.example.com http://localhost:3000 --outpost nas --state-dir ~/tnl-edge
./tnl route ls --state-dir ~/tnl-edge
```

The Service URL is `http://`, a host and an optional port (default 80), with no path. The host is resolved on the Outpost's network, so `localhost` there is the Outpost host itself. The Hostname must be exact (no wildcard), inside a Zone you added, and not held by another Route.

The Route is saved and swapped in at once. Caddy is not reloaded, so other Routes' connections are not touched.

### 7. The first request

```sh
curl -v https://app.example.com/
```

The first request for a new Hostname waits while Caddy gets its certificate from the ACME CA, usually a few seconds. Port 80 answers the CA's challenge, so it must be reachable from the internet. After that the certificate is cached in the state directory and renewed in the background. Requests to `http://app.example.com` get a 308 redirect to HTTPS.

If something is wrong, `tnl edge` logs it. Add `--log-level debug` for more.

## Development

```sh
go vet ./... && go vet -tags nomysql,outpost ./...
go test -tags nomysql ./...
scripts/check-outpost-deps.sh
```

The tests run an Edge, Outposts, Services and an ACME CA (Pebble) in one process, with real WireGuard over loopback and no root.

## Licence

See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
