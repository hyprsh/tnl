# Outposts

An Outpost is a tnl process on the network where your Services live. It dials the Edge, and the Edge sends Visitors' requests down that connection. You manage Outposts on the Edge host. Run the commands below with `sudo tnl ...` on a host, or `docker exec tnl tnl ...` when the Edge runs in a container.

An Edge that declares its Outposts in its settings refuses `outpost add`, `rekey` and `rm`: see [Declaring the Edge](declaring-the-edge.md). The rest of this page is the token flow.

## Add an Outpost

```sh
sudo tnl outpost add laptop
```

The name is one DNS label, is unique, and never changes. The command prints three lines:

<!-- x-release-please-start-version -->

```
<token>
curl -fsSL https://github.com/hyprsh/tnl/releases/download/v0.2.0/install.sh | sudo sh -s -- outpost --token <token>
curl -fsSL https://github.com/hyprsh/tnl/releases/download/v0.2.0/install.sh | sh -s -- outpost --token <token>
```

<!-- x-release-please-end -->

The Edge doesn't know the Outpost's OS, so it prints both install lines. Run the first (with `sudo`) on Linux, which installs the systemd unit. Run the second (without) on macOS, where the install is per-user and `sudo` would make the binary root's. A note on stderr says which is which.

- **The token works once** and expires after one hour. `--ttl 30m` or `--ttl 2h` changes that.
- **The install line is pinned to the Edge's version.** The Outpost installs the same release as the Edge, which is the one the Edge accepts. A build with no release version (`dev`) can't pin: it prints both lines with `releases/latest` and a note on stderr that they are not pinned.
- **The token is shown once** and tnl never stores it. If you lose it, `rekey` makes another.

## Join

Run the install line for the Outpost host's OS. Without root or systemd, install the binary and run `tnl connect --token <token>` yourself.

### Under systemd

`install.sh` writes `/etc/tnl/outpost.env` with `TNL_TOKEN=<token>` and starts the unit. The token stays in the file, which is harmless: it works once, and a restart with it still set ignores it, because the Outpost's state was made from it.

`install.sh` never overwrites an existing `outpost.env`. To join with a new token on a host that has one, for example after a `rekey`, paste the new token into `TNL_TOKEN` in `/etc/tnl/outpost.env` and run `sudo systemctl restart tnl-outpost`.

### Under Docker Compose

<!-- x-release-please-start-version -->

```yaml
services:
  tnl-outpost:
    image: ghcr.io/hyprsh/tnl-outpost:0.2.0
    container_name: tnl-outpost
    restart: unless-stopped
    environment:
      TNL_TOKEN: <token>
    volumes: ["tnl-outpost:/var/lib/tnl-outpost"]
    networks: [apps]
networks:
  apps:
    external: true
volumes:
  tnl-outpost:
```

<!-- x-release-please-end -->

- **The named volume holds the Outpost's state.** Without it a restart has nothing to reconnect from, and the used token is refused.
- **Leave `TNL_TOKEN` set.** After the first join a restart ignores it. To join with a new token, change it and recreate the container: a different token joins again and replaces the state.
- **Services are reached by container name,** on the network the Outpost shares with them: `http://immich:2283`. `localhost` inside the container is the container, not the host.
- **A bind mount** for the state directory needs `chown 65532`.

## List the Outposts

```sh
sudo tnl outpost ls
```

One row per Outpost, sorted by name:

- **STATE** is `online` (heard from in the last 60 s), `offline`, `never connected` (joined or declared, but the Edge has no record of it ever connecting; it turns `offline` 60 s after the Edge starts), `incompatible` (its version is outside the Edge's window) or `not joined` (added, token unused). A declared Outpost is never `not joined`, and is marked "(declared)".
- **LAST SEEN** is relative ("4s ago"). A `not joined` row shows "token expires in 42m" or "expired" instead. For up to 60 s after an Edge restart, an Outpost that has connected before and that the Edge has not heard from yet shows `online` with "–". One that has never connected shows `never connected` with "–".
- **ENDPOINT** and **VERSION** are what the Edge last heard, "–" when unknown. **ROUTES** is a count, or "3 declared" for an Outpost that declares its Routes.

`--json` prints the same as JSON, the stable form for scripts: times are RFC 3339, durations are seconds, and the fields only ever grow within a major version. The tables may change in any release.

## Remove an Outpost

```sh
sudo tnl outpost rm laptop
```

- **It refuses while Routes point at the Outpost,** lists them, and changes nothing. `--with-routes` removes those Routes too.
- **It drops the Outpost's WireGuard peer and closes its connections.** Streams that were open through it end at once.
- **Removing an Outpost that hasn't joined cancels its token.**
- **An Outpost can't remove itself,** or any other. These commands work only on the Edge host.

The removed Outpost's process keeps running and logs that it gets no answer from the Edge. Stop it and delete its state directory.

## Rekey an Outpost

```sh
sudo tnl outpost rekey laptop --ttl 30m
```

Rekey revokes the Outpost's current key at once: its Tunnel stops, and Routes through it fail until it joins again. It prints a new token and install line, like `add` does. Joining with the new token keeps the Outpost's name, Tunnel address and Routes.

Use it when a key may have leaked, when an Outpost host is replaced, or when the state of an Outpost is lost. Run the new token on the Outpost as in [Join](#join). There is no automatic rotation.

The Edge's own WireGuard key never rotates in v1. Changing it means rekeying every Outpost.
