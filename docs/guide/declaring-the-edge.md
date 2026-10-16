# Declaring the Edge

An Edge can take its Zones and its Outposts from its settings, so that one Compose file on the Edge and one on each Outpost hold the whole setup. No `docker exec tnl tnl outpost add` round trip, and no state volume on the Outposts.

You need three things the Edge cannot make for itself, all made offline:

1. **The Edge's private key,** so its public key is known before it first starts. `tnl keygen` prints one in `wg genkey` format:

   ```sh
   docker run --rm ghcr.io/hyprsh/tnl:1.4.2 keygen > tnl-key
   sudo chown 65532 tnl-key && sudo chmod 600 tnl-key   # the Edge runs as uid 65532 and reads the file as it is on the host
   ```

2. **A line and a credential for each Outpost,** from the key:

   ```sh
   docker run --rm -v "$PWD/tnl-key:/key:ro" ghcr.io/hyprsh/tnl:1.4.2 \
     credential nas --address edge.example.com --key-file /key
   ```

   ```
   TNL_OUTPOST_NAS=nas 6WvT…=
   TNL_CREDENTIAL=tnlc1.…
   ```

   The first line goes in the Edge's settings. The credential goes to the Outpost. `--wireguard-port N` names a port other than 51820. The command never contacts an Edge, so it runs anywhere the full build does.

3. **The names the Outposts dial.** The Edge's `--address` and WireGuard port are baked into every credential. Changing either means minting every credential again, which is one more reason to give `--address` a DNS name.

## The Edge

```yaml
services:
  tnl:
    image: ghcr.io/hyprsh/tnl:X.Y.Z
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

([`examples/compose/edge.yaml`](../../examples/compose/edge.yaml).) Without Compose, the same settings are flags: `tnl edge --address … --key-file … --zone "example.com" --outpost "nas 6WvT…="`, with `--zone` and `--outpost` repeated for more.

### The lines

- `TNL_ZONE_<LABEL>=<zone> [--wildcard PROVIDER (--token-file PATH | --token VALUE)]` holds the arguments of `tnl zone add`, plus where the wildcard token comes from.
- `TNL_OUTPOST_<LABEL>=<name> <public-key>`. The key is standard base64, as `wg pubkey` prints it.
- `<LABEL>` is letters, digits and `_`, and names the line in messages. Words are split on whitespace, with no quoting.
- Use the variables or the flags of one kind, never both.
- `--token` works in `TNL_ZONE_*` variables only, where the token sits in the environment anyway. A `--zone` flag line takes `--token-file`. `tnl zone add` keeps its prompt.

A wildcard Zone in Compose:

```yaml
environment:
  TNL_ZONE_MAIN: example.com --wildcard cloudflare --token-file /run/secrets/cloudflare
```

### The Edge's key

`--key-file` (`TNL_KEY_FILE`) names a file with the key. `TNL_KEY` holds the key itself. Setting both is refused.

- A new Edge takes the key as its own. It logs the key's fingerprint, as it always does.
- A `state.json` that holds another key refuses the start: `state.json holds a different Edge key than TNL_KEY_FILE`. Neither key appears in the message.
- Without either, the Edge generates its key at first start, as before. You can then read its public key from the log, but you cannot mint credentials in advance.
- Rotating the Edge's own key is not supported in v1.

## The Outpost

```yaml
services:
  tnl-outpost:
    image: ghcr.io/hyprsh/tnl-outpost:X.Y.Z
    container_name: tnl-outpost
    restart: unless-stopped
    environment:
      TNL_CREDENTIAL_FILE: /run/secrets/tnl-credential
      TNL_ROUTE_PHOTOS: photos.example.com http://immich:2283
    secrets: [tnl-credential]
    networks: [apps]
secrets:
  tnl-credential:
    file: ./tnl-credential
networks:
  apps:
    external: true
```

([`examples/compose/outpost.yaml`](../../examples/compose/outpost.yaml).) Put the part after `TNL_CREDENTIAL=` in `./tnl-credential`.

- **It connects from the first packet,** with no join exchange, and writes no `state.json`. The state directory holds only `tnl.sock`, so the container needs no volume.
- **`--credential-file`** (`TNL_CREDENTIAL_FILE`), or `TNL_CREDENTIAL` holding the credential itself. Setting both is refused, and so is `--token` together with a credential. The credential is never a flag value.
- **A `state.json` left in the directory** from an earlier join is left alone and ignored, with an `info` line.
- **The Outpost declares its Routes** as in [Declaring Routes](declaring-routes.md), or uses `tnl route`. Routes are never declared on the Edge, and `tnl edge` refuses `TNL_ROUTE_*`.
- **The credential is the Outpost's identity and never expires.** Anyone holding it connects as that Outpost until the Edge's line for it changes. Keep it like a password. Two running copies of one credential fight over the Outpost's endpoint, and v1 does not detect it.
- **Secret files are bind-mounted as they are on the host,** so each must be readable by uid 65532.

## What the Edge does at start

The Edge checks every line, then applies them as a diff by name in one locked change, before it builds Caddy's config. Each declared item is saved in `state.json` and marked as declared.

- **Unchanged lines change nothing.**
- **A new Zone line** is a `zone add`. A changed mode or token is a `zone edit`. A Zone that already existed (made with the CLI) under a declared name is adopted and keeps its Routes.
- **A new Outpost line** adds the Outpost at the Tunnel address derived from its name, already joined, with no token. The derived address is a fixed hash of the name into `100.64.0.0/10`, so the Outpost knows it before its first packet, and it stays put when the key changes or the lines are reordered.
- **A changed key** is a `rekey`: the old key stops working at once, and the Outpost keeps its name, Tunnel address, Routes and Declaration. Declaring the name of a CLI Outpost adopts it the same way and moves it to its derived address.
- **A Zone line that is gone** is `zone rm --with-routes`: Routes from a Declaration go back to waiting, and Routes that a parent Zone still contains move to it. If a Route made with the CLI would end up outside every Zone, the Edge exits 78 instead, and `state.json` stays as it was:

  ```
  example.com is no longer declared, but app.example.com, git.example.com are in it; remove them or declare the Zone again
  ```

- **An Outpost line that is gone** is `outpost rm --with-routes`.
- **The first start with declared lines** removes the CLI's Zones and Outposts that are not declared, by the same rules. Each removal is logged at `warn`, each other change at `info`.

### One source per kind

While any `TNL_ZONE_*` or `--zone` is set, the declared Zones are the Edge's Zones, and `tnl zone add`, `edit` and `rm` are refused: `Zones are declared; change TNL_ZONE_* and restart the Edge`. `TNL_OUTPOST_*` works the same way for `tnl outpost add`, `rekey` and `rm`, and a declaring Edge mints no join tokens: no Outpost joins.

### Removing every line

An Edge started with no lines of a kind hands that kind back to the CLI and removes nothing. The Zones or Outposts stay, lose their declared mark, and a `warn` says so. This is deliberate: an Edge started once without its environment, from a forgotten `.env` or by hand, would otherwise wipe every Zone, Outpost and Route. To remove one, follow with `tnl zone rm` or `tnl outpost rm`.

### A wildcard token that fails its check

After the change is saved, a new or changed wildcard token gets the same check as `tnl zone add`. If it fails, the Edge logs an `error`, keeps the Zone and keeps running. The check is repeated after 5 minutes, 30 minutes and then hourly, with an `error` each time and an `info` when it passes. Caddy retries the certificate like any failed issuance. A DNS provider's outage cannot take the Edge down.

## Rotating a credential

Mint a new one for the same name, change both lines (the Edge's `TNL_OUTPOST_<NAME>` and the Outpost's credential), and restart both sides. The Outpost keeps its Tunnel address, Routes and Declaration. The old credential stops working at once.

## Errors

Every problem with a line exits with status 78 and names the variable: `TNL_ZONE_MAIN: unknown option --wildcrd`. A `--zone` line is named by its first word. The Edge refuses to start on:

- a syntax error or an unknown option
- an invalid Zone, name or key
- the same Zone or name on two lines, or the same key on two Outposts
- a wildcard line without exactly one of `--token-file` and `--token`, an unknown provider, or a token file that is missing, unreadable or empty
- two names whose derived Tunnel addresses collide, naming both: rename one
- a key that doesn't match `state.json`'s
- both forms of one setting
- `TNL_ROUTE_*`: `TNL_ROUTE_PHOTOS: Routes are declared on the Outpost; set TNL_ROUTE_* there`

**No message carries a secret.** A bad key, token or credential is reported as bad and never shown, the "did you mean" for an unknown `TNL_*` variable lists names only, and a token is replaced with `[redacted]` wherever one could reach a log or an error. Secrets in variables are still visible to `docker inspect`: prefer `--key-file`, `--token-file` and `--credential-file` with Compose `secrets:`.

## Backups

`tnl backup` and a restore work as before. Startup settings are not in the archive, so keep `--key-file` beside the backup. Declared Zones and Outposts are brought up to date from the settings at start, whatever the restored `state.json` held.
