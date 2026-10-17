# tnl

tnl puts a Service on your home or office network at a public `https://` address. The Edge, a small server with a public IP, terminates HTTPS. An Outpost, running on the network where the Service lives, joins the Edge over userspace WireGuard and forwards the traffic. Neither side needs root, a TUN device or a firewall hole at the Outpost.

The design is in [`docs/spec/v1.md`](docs/spec/v1.md), the terms in [`GLOSSARY.md`](GLOSSARY.md).

> The tracer-bullet feature set: one `http://` Service per Route, a certificate for each Hostname. Installing, ports, systemd and Docker are in [`docs/guide/install.md`](docs/guide/install.md); upgrading is in [`docs/guide/upgrading.md`](docs/guide/upgrading.md). `tnl help` explains every command; shell completion is in [`docs/guide/completion.md`](docs/guide/completion.md).

## Quick start

You need two machines, or two terminals on one for a trial:

- **The Edge host** has a public IP and a DNS name, here `edge.example.com`.
- **The Outpost host** is on the network where the Service runs. Here the Service is `localhost:3000` on that host, and the Outpost is called `nas`.

### 1. Open the ports

On the Edge host's firewall and your cloud provider's security group, open 80/tcp, 443/tcp, 443/udp and 51820/udp. The [install guide](docs/guide/install.md#ports-and-firewall) says what each is for. The Outpost host needs no inbound port.

Point the DNS names at the Edge host: `edge.example.com` for the Outposts, and `app.example.com` (or a wildcard record) for the Hostnames you will serve.

### 2. Install the Edge

On the Edge host:

```sh
curl -fsSL https://github.com/hyprsh/tnl/releases/latest/download/install.sh | sudo sh -s -- edge --address edge.example.com
```

`--address` is the DNS name Outposts dial. The script installs the binary and a systemd unit and starts it. Settings go in `/etc/tnl/edge.env`, such as `TNL_ACME_EMAIL`, and `tnl edge -h` lists them. Certificates come from Let's Encrypt production unless `TNL_ACME_CA` names another ACME directory, such as Let's Encrypt staging while you try things out. Docker Compose works as well; see the install guide.

The Edge keeps everything in its state directory, in `state.json`, and builds Caddy's config from it at every start. Routes survive a restart.

### 3. Add a Zone and an Outpost

On the Edge host:

```sh
sudo tnl zone add example.com
sudo tnl outpost add nas
```

`zone add` says which DNS zone the Edge may serve Hostnames in. `outpost add` prints a join token. The token is shown once and is never stored. It holds the key the Outpost joins with, so send it over a channel you trust.

### 4. Connect the Outpost

On the Outpost host, with the token:

```sh
curl -fsSL https://github.com/hyprsh/tnl/releases/latest/download/install.sh | sudo sh -s -- outpost --token <the token>
```

`tnl outpost add` prints this line pinned to the Edge's version, and a second one without `sudo` for a macOS Outpost.

This installs the Outpost-only build and a systemd unit, and starts it. The Outpost saves what it needs in its state directory, so a restart needs no token. Without root or systemd (macOS, say) the script installs only the binary, to `~/.local/bin`, and prints the `tnl connect --token …` line to run in a terminal or under your own supervisor.

### 5. Add a Route

On the Edge host:

```sh
sudo tnl route add app.example.com http://localhost:3000 --outpost nas
sudo tnl route ls
```

The Service URL is `http://`, a host and an optional port (default 80), with no path. The host is resolved on the Outpost's network, so `localhost` there is the Outpost host itself. The Hostname must be exact (no wildcard), inside a Zone you added, and not held by another Route.

The Route is saved and swapped in at once. Caddy is not reloaded, so other Routes' connections are not touched.

### 6. The first request

```sh
curl -v https://app.example.com/
```

The first request for a new Hostname waits while Caddy gets its certificate from the ACME CA, usually a few seconds. Port 80 answers the CA's challenge, so it must be reachable from the internet. After that the certificate is cached in the state directory and renewed in the background. Requests to `http://app.example.com` get a 308 redirect to HTTPS.

If something is wrong, the Edge logs it: `tnl logs` (or `journalctl -u tnl-edge`). Add `TNL_LOG_LEVEL=debug` to `/etc/tnl/edge.env` for more.

## Development

Build from source with Go 1.27 or newer; there is no cgo and no xcaddy: `go build -tags nomysql -o tnl ./cmd/tnl`, or `-tags nomysql,outpost` for the Outpost-only build.

```sh
go vet ./... && go vet -tags nomysql,outpost ./...
go test -tags nomysql ./...
scripts/check-outpost-deps.sh
```

The tests run an Edge, Outposts, Services and an ACME CA (Pebble) in one process, with real WireGuard over loopback and no root.

## Licence

See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
