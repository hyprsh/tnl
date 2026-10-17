# Installing tnl

Each release ([github.com/hyprsh/tnl/releases](https://github.com/hyprsh/tnl/releases)) holds archives for two builds, a `SHA256SUMS` file, `install.sh` and the systemd units. The full build runs the Edge (and can also run an Outpost). The Outpost-only build is smaller and has no Edge. Two images carry the same builds.

Pick one of:

- [systemd with `install.sh`](#install-with-installsh), on a Linux host. This is the usual way.
- [Docker Compose](#docker-compose), equally supported.

## Ports and firewall

`install.sh` does not touch your firewall (it does raise two sysctl limits, see [UDP buffers](#udp-buffers)). Open these on the Edge host's firewall and your cloud provider's security group:

| Port | Protocol | For |
|---|---|---|
| 80 | TCP | ACME HTTP-01 challenges, and a redirect to HTTPS |
| 443 | TCP | HTTPS, HTTP/1.1 and HTTP/2 |
| 443 | UDP | HTTP/3 |
| 51820 | UDP | WireGuard, which Outposts dial |

An Outpost host needs no inbound port. It only has to reach the Edge's UDP port 51820.

Point DNS at the Edge host: `edge.example.com` for the Outposts, and the Hostnames you serve (or a wildcard record) for Visitors.

## Install with `install.sh`

On the Edge host:

```sh
curl -fsSL https://github.com/hyprsh/tnl/releases/latest/download/install.sh | sudo sh -s -- edge --address edge.example.com
```

`--address` is the DNS name Outposts dial. On an Outpost host, with the token that `sudo tnl outpost add <name>` printed on the Edge:

```sh
curl -fsSL https://github.com/hyprsh/tnl/releases/latest/download/install.sh | sudo sh -s -- outpost --token <token>
```

On macOS leave out `sudo`: the install is per-user (see below), and `sudo` would give root the binary.

```sh
curl -fsSL https://github.com/hyprsh/tnl/releases/latest/download/install.sh | sh -s -- outpost --token <token>
```

`releases/latest/download/install.sh` installs the latest release. To pin a version, use `releases/download/vX.Y.Z/install.sh`: each release attaches its own script, which installs that release's version. `tnl outpost add` prints both lines (Linux with `sudo`, macOS without), pinned to the Edge's version.

### Without root or systemd

If you are not root, or the host has no systemd (macOS, for one), the script installs only the binary, to `~/.local/bin/tnl`, and prints the command to run, for an Outpost `tnl connect --token <token>`. Run it in a terminal, or under your own supervisor (a launchd job on macOS). The Outpost keeps its state in `~/.local/state/tnl-outpost`.

macOS has Outpost builds only. The Edge runs on Linux.

### What the script does

1. Picks the archive for the role and platform: the full build for the Edge, the Outpost-only build for an Outpost.
2. Downloads it and `SHA256SUMS`, and refuses to go on if the archive's checksum does not match. The checksum only catches a corrupted download. The proof of origin is the release's attestation: `gh attestation verify <archive> --repo hyprsh/tnl`.
3. Swaps the binary into `/usr/local/bin/tnl` atomically.
4. Installs the role's unit to `/etc/systemd/system/tnl-edge.service` or `tnl-outpost.service`, overwriting an older copy.
5. Writes `/etc/tnl/edge.env` (`TNL_ADDRESS`) or `/etc/tnl/outpost.env` (`TNL_TOKEN`), mode 0600, but never overwrites an existing one.
6. Writes `/etc/sysctl.d/60-tnl.conf` and applies it with `sysctl -p`, as [UDP buffers](#udp-buffers) explains. It keeps an existing file, and on an Outpost writes it only if the limits are below 7500000. It prints a line saying what it set. If it cannot apply the file, it warns and goes on.
7. Runs `systemctl daemon-reload` and `systemctl enable --now` on the unit, and restarts whichever tnl units were already running.
8. If the unit is not running a second later, prints its last 20 log lines and exits with an error.

The script never replaces a full build with the Outpost build, so a host that runs both roles keeps the full one (an Outpost install there uses the full archive).

### UDP buffers

The Edge's HTTP/3 listener (quic-go) and the WireGuard socket (wireguard-go, on the Edge and on Outposts) ask Linux for 7 MiB socket buffers. Linux caps a request at `net.core.rmem_max` and `net.core.wmem_max`, which default to about 208 KiB. With a buffer that small, packets are dropped under load and HTTP/3 slows down. At start the Edge and an Outpost open a UDP socket, ask for 7 MiB buffers the way quic-go does (including the forced request that a process with `CAP_NET_ADMIN` is allowed) and read back what they got. This measures the socket itself, so it works in a container on a bridge network, which cannot read the host's `net.core` limits. If either buffer comes back below 7 MiB (7340032), they log one `warn` line that names both settings and the value 7500000 and points here, as in `the UDP buffer limits are too low`. tnl switches off quic-go's own `failed to sufficiently increase receive buffer size` line. A host where tnl can't open the socket to measure it gets no line.

`install.sh` writes this to `/etc/sysctl.d/60-tnl.conf` and applies it:

```
net.core.rmem_max = 7500000
net.core.wmem_max = 7500000
```

If you do not use `install.sh`, put those two lines in a file under `/etc/sysctl.d/` yourself and run `sudo sysctl -p <file>`, then restart tnl, since a socket takes its buffer size when it opens. This also applies to an Edge or Outpost in Docker: the limits are the host's, and a container cannot set `net.core.*` itself or always read it, so set them on the Docker host and then recreate the container. The warning still appears there, since it measures the socket.

macOS needs nothing.

### Upgrading

Run the install line again, and pin a version if you want one. It is the upgrade: the binary is swapped atomically and the running units restart. Upgrade the Edge first, then each Outpost whenever convenient. [Upgrading](upgrading.md) says which versions work together and which image tags are safe for Outposts.

### systemd

The units are tnl's files, and every run of `install.sh` overwrites them. Change them with a drop-in:

```sh
sudo systemctl edit tnl-edge
```

Both units run as a dynamic user with a state directory (`/var/lib/tnl` for the Edge, `/var/lib/tnl-outpost` for an Outpost). The Edge unit has only the capability to bind ports 80 and 443. Settings go in the env file, one `TNL_*` variable per line (`tnl edge -h` lists them), for example `TNL_ACME_EMAIL=you@example.com`. Then `sudo systemctl restart tnl-edge`.

The Outpost's token stays in `outpost.env`. That is harmless: it works once, and a restart with it still set ignores it. To rekey, paste the new token into the file and restart.

Logs: `tnl logs` for the recent lines, or `journalctl -u tnl-edge` and `journalctl -u tnl-outpost` for all of them. To run a command against the Edge, use `sudo tnl zone add example.com`.

An Edge and an Outpost on one host do not collide: the Edge uses `/var/lib/tnl`, an Outpost under systemd or Docker uses `/var/lib/tnl-outpost`, and any other Outpost uses `~/.local/state/tnl-outpost`.

## Docker Compose

Two images, both multi-arch (linux/amd64 and linux/arm64), built on distroless and running as uid 65532:

- `ghcr.io/hyprsh/tnl`, the full build, state in `/var/lib/tnl`
- `ghcr.io/hyprsh/tnl-outpost`, the Outpost-only build, state in `/var/lib/tnl-outpost`

Tags are `X.Y.Z`, `X.Y`, `X` and `latest`. The examples pin an exact `X.Y.Z`, which is what you should do. Before 1.0, `0.3` (the minor you run) is the safe moving tag for Outposts, and `latest` and a bare `0` are not.

- [`examples/compose/edge.yaml`](../../examples/compose/edge.yaml)
- [`examples/compose/outpost.yaml`](../../examples/compose/outpost.yaml)

```sh
docker compose -f edge.yaml up -d
docker exec tnl tnl zone add example.com
docker exec tnl tnl outpost add nas
```

The commands above are the CLI way. The example files are the declared way: the Zones and Outposts are in the Edge's environment and each Outpost holds a credential, so nothing needs `docker exec`. See [Declaring the Edge](declaring-the-edge.md).

Things to know:

- **Commands:** `docker exec tnl tnl …` in a container, where `sudo tnl …` is on a host. The image's `TNL_STATE_DIR` carries over into the exec.
- **Volumes:** a named volume inherits ownership by uid 65532 from the image. A bind mount needs `chown 65532` on the directory first.
- **Secret files** are bind-mounted as they are on the host, so each must be readable by uid 65532.
- **UDP buffers:** set `net.core.rmem_max` and `net.core.wmem_max` to at least 7500000 on the Docker host, as [UDP buffers](#udp-buffers) says.
- **Ports:** the Edge publishes its ports rather than using host networking. On a bridge network Docker lets a non-root process bind ports below 1024. Without Docker's IPv6 enabled, IPv6 Visitors arrive through the userland proxy and lose their address.
- **Stopping:** `stop_grace_period: 20s` gives the Edge time to drain.
- **The Outpost reaches Services by container name** on a shared network, such as `http://immich:2283`. Inside the Outpost's container, `localhost` is the container, not the host.
- **The token** goes in `TNL_TOKEN`. It works once, and the volume keeps what the Outpost saves. An Outpost declared by its Edge uses `TNL_CREDENTIAL_FILE` instead and needs no volume.
- **Routes** can be declared in `TNL_ROUTE_*` variables, as the example does; see [Declaring Routes on the Outpost](declaring-routes.md).

## Help and shell completion

`tnl help` explains every command. `install.sh` installs no shell completion; [Help and shell completion](completion.md) says how to add it for bash, zsh or fish.

## Uninstalling

`install.sh` has no uninstall. Stop and remove what it added:

```sh
sudo systemctl disable --now tnl-edge tnl-outpost   # whichever you installed
sudo rm /etc/systemd/system/tnl-edge.service /etc/systemd/system/tnl-outpost.service
sudo systemctl daemon-reload
sudo rm /usr/local/bin/tnl
sudo rm -r /etc/tnl
sudo rm /etc/sysctl.d/60-tnl.conf   # the buffer limits; they stay in effect until a reboot or sysctl --system
sudo rm -r /var/lib/tnl /var/lib/tnl-outpost   # the Edge's key, Routes and certificates, or the Outpost's key
```

Without root: delete `~/.local/bin/tnl` and `~/.local/state/tnl-outpost`. With Compose: `docker compose down -v`.

Removing the Edge's state directory deletes its WireGuard key, so every Outpost has to be added again.
