# Backup and restore

`tnl backup` writes one archive of the Edge's state. Restoring it brings every Outpost back without rekeying, on the same VPS or a new one.

## Back up

Run it on the Edge host, as the user the Edge runs as (or with `sudo`):

```
tnl backup /var/backups/tnl.tar
tnl backup - | age -r <recipient> > tnl.tar.age
```

`<file>` is created with mode 0600. `-` writes the archive to stdout. The Edge builds it while it runs, so nothing needs stopping. Pass `--state-dir` (or set `TNL_STATE_DIR`) if the Edge doesn't use `/var/lib/tnl`.

The archive holds:

- `state.json`, copied under the Edge's lock, so it is never caught mid-change
- `caddy/`, Caddy's storage (certificates and the ACME account), without its lock and temp files

A certificate whose key doesn't match is left out, and `tnl backup` names it on stderr. Caddy obtains it again after a restore.

The archive is secret: it holds the Edge's WireGuard private key and the ACME account key, and it is not encrypted. tnl has no encryption, schedule or history. Pipe the archive into `age` or restic, and run it from cron.

Startup settings are not in the archive, so neither is `--key-file`. Keep that file beside the backup.

## Restore

1. Stop the Edge.
2. Replace the contents of the state directory with the archive's: `tar -xf tnl.tar -C /var/lib/tnl`. The directory must be mode 0700.
3. Start the Edge with the same `--address` and WireGuard port.

On a new VPS, move the `--address` DNS name to it first. Outposts that joined before the backup reconnect on their own, without rekeying.

What you lose:

- Outposts added or rekeyed after the backup need `outpost add` or `rekey` again.
- Route and Zone changes made after the backup are gone.

An Edge migrates an older backup when it starts, as it does its own state, and keeps the old file as `state.json.v<n>`; see [The Edge's state file](upgrading.md#the-edges-state-file). It refuses a newer one, so restore with a tnl at least as new as the one that took the backup.

## Restore without `caddy/`

Leave `caddy/` out, or lose it, and the Edge still starts from `state.json` alone. It costs you:

- Every certificate is obtained again, and Let's Encrypt's rate limits apply. Many Hostnames at once can hit them.
- A lost ACME account key means registering a new account.
