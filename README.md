# tnl

tnl puts Services on your home or office network at public `https://` addresses. The Edge, a small server with a public IP, terminates HTTPS. An Outpost, running on the network where the Services live, dials out to the Edge over userspace WireGuard and forwards the traffic. The Outpost's network needs no open port, and neither side needs root or a TUN device.

The design is in [`docs/spec/v1.md`](docs/spec/v1.md), the terms in [`GLOSSARY.md`](GLOSSARY.md).

## What you need

- **An Edge host:** Linux with Docker and a public IP.
- **A wildcard DNS record,** `*.example.com`, pointing at the Edge host. It covers the name Outposts dial, `edge.example.com`, and every Hostname you serve.
- **Open ports on the Edge host:** 80/tcp, 443/tcp, 443/udp and 51820/udp, on its firewall and your cloud provider's security group. The [install guide](docs/guide/install.md#ports-and-firewall) says what each is for.
- **An Outpost host:** any machine with Docker on the Services' network. It needs no inbound port, only outbound UDP to the Edge's port 51820.
- **Optional:** raise the UDP buffer limits on both Docker hosts, as [UDP buffers](docs/guide/install.md#udp-buffers) says, or HTTP/3 slows under load.

## Quick start

This serves two Services from one Outpost called `nas`: Jellyfin at `media.example.com` and Uptime Kuma at `status.example.com`.

<!-- x-release-please-start-version -->

### 1. Make the keys, on the Edge host

```sh
docker run --rm ghcr.io/hyprsh/tnl:0.1.2 keygen > tnl-key
docker run --rm -v "$PWD/tnl-key:/key:ro" ghcr.io/hyprsh/tnl:0.1.2 \
  credential nas --address edge.example.com --key-file /key
sudo chown 65532 tnl-key && sudo chmod 600 tnl-key
```

`tnl-key` is the Edge's private key. `credential` prints two lines:

```
TNL_OUTPOST_NAS=nas 6WvT…=
TNL_CREDENTIAL=tnlc1.…
```

The first goes in the Edge's `compose.yaml`. The part after `TNL_CREDENTIAL=` is the Outpost's credential: it never expires, so keep it like a password.

### 2. Start the Edge

Next to `tnl-key`, save this as `compose.yaml`, with your address, Zone and the `TNL_OUTPOST_NAS` line from step 1:

```yaml
# The Edge. Replace the address, the Zone and the Outpost's line with yours.
services:
  tnl:
    image: ghcr.io/hyprsh/tnl:0.1.2
    container_name: tnl
    restart: unless-stopped
    stop_grace_period: 20s
    environment:
      TNL_ADDRESS: edge.example.com
      TNL_KEY_FILE: /run/secrets/tnl-key
      TNL_ZONE_MAIN: example.com
      TNL_OUTPOST_NAS: nas 6WvT…=
    secrets: [tnl-key]
    ports: ["80:80/tcp", "443:443/tcp", "443:443/udp", "51820:51820/udp"]
    volumes: ["tnl:/var/lib/tnl"]
secrets:
  tnl-key:
    file: ./tnl-key
volumes:
  tnl:
```

```sh
docker compose up -d
```

### 3. Start the Outpost

On the Outpost host, put the credential in `tnl-credential`:

```sh
echo 'tnlc1.…' > tnl-credential
sudo chown 65532 tnl-credential && sudo chmod 600 tnl-credential
```

Next to it, save this as `compose.yaml`:

```yaml
# An Outpost and the two Services it forwards to, on Compose's default network.
services:
  tnl-outpost:
    image: ghcr.io/hyprsh/tnl-outpost:0.1.2
    container_name: tnl-outpost
    restart: unless-stopped
    environment:
      TNL_CREDENTIAL_FILE: /run/secrets/tnl-credential
      TNL_ROUTE_MEDIA: media.example.com http://jellyfin:8096
      TNL_ROUTE_STATUS: status.example.com http://uptime-kuma:3001
    secrets: [tnl-credential]
  jellyfin:
    image: jellyfin/jellyfin
    restart: unless-stopped
    volumes: ["jellyfin:/config"]
  uptime-kuma:
    image: louislam/uptime-kuma:1
    restart: unless-stopped
    volumes: ["uptime-kuma:/app/data"]
secrets:
  tnl-credential:
    file: ./tnl-credential
volumes:
  jellyfin:
  uptime-kuma:
```

```sh
docker compose up -d
```

<!-- x-release-please-end -->

Each `TNL_ROUTE_*` line is a Route: a Hostname and the Service URL behind it. The Outpost reaches each Service by its Compose service name, and neither app publishes a port. To serve another Service, add a line and run `docker compose up -d` again.

### 4. The first request

```sh
curl -v https://media.example.com/
```

The first request for a Hostname waits a few seconds while the Edge gets its certificate from Let's Encrypt. Port 80 must be reachable from the internet for that. Plain `http://` gets a redirect to HTTPS.

If something is wrong, `docker exec tnl tnl logs` on the Edge and `docker exec tnl-outpost tnl logs` on the Outpost show what happened. [Troubleshooting](docs/guide/troubleshooting.md) covers the usual causes.

## Next

- [Installing](docs/guide/install.md): Docker Compose in detail, image tags, and `install.sh` with systemd for a host without Docker.
- [Declaring the Edge](docs/guide/declaring-the-edge.md): the keys, credentials and `TNL_ZONE_*` and `TNL_OUTPOST_*` lines used above.
- [Declaring Routes on the Outpost](docs/guide/declaring-routes.md): the `TNL_ROUTE_*` lines and their options.
- [Zones](docs/guide/zones.md), including a wildcard certificate through your DNS provider.
- [Outposts](docs/guide/outposts.md) and [Routes](docs/guide/routes.md) managed with the CLI instead.
- [Upgrading](docs/guide/upgrading.md), [Backup](docs/guide/backup.md), [Monitoring](docs/guide/monitoring.md), [Troubleshooting](docs/guide/troubleshooting.md) and [shell completion](docs/guide/completion.md). `tnl help` explains every command.

## Development

Build from source with Go 1.27 or newer; there is no cgo and no xcaddy: `go build -tags nomysql -o tnl ./cmd/tnl`, or `-tags nomysql,outpost` for the Outpost-only build.

```sh
scripts/check.sh
```

runs every check CI runs. The tests run an Edge, Outposts, Services and an ACME CA (Pebble) in one process, with real WireGuard over loopback and no root.

## Licence

See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
