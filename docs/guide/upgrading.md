# Upgrading

Upgrade the Edge first, then each Outpost whenever you reach its machine. The Edge judges every Outpost by its version, and an Outpost outside the Edge's window is **Incompatible**.

## How to upgrade

There is no `tnl upgrade` and no self-updater. On a host with systemd, run the install line again (see [Installing](install.md#upgrading)); with Docker, change the image tag and recreate the container.

The order is the **Edge first, then each Outpost** whenever you reach its machine. Within a 0.x minor, or within a major, either order works, because the window takes both sides. **At a major, the order is required:** the new Edge still takes the previous major's Outposts, but an Outpost of a new major refuses an Edge of an older one.

## Which tags are safe for Outposts

Images are tagged `X.Y.Z`, `X.Y`, `X` and `latest`. For an Outpost, which a later Edge upgrade must not strand:

- **Safe:** an exact `X.Y.Z`, and the `1` tag (before 1.0, the `0.3` tag). They move only inside the Edge's window.
- **Not safe:** `latest`, and a bare `0`. They jump to the next 0.x minor, which the Edge may not accept yet; `latest` also jumps to the next major.

Pin an exact `X.Y.Z` wherever you want to choose the moment of an upgrade.

## What the release notes say

- They open with the compatibility line, the same one `tnl version` prints.
- A major's notes lead with an **Upgrading from 1.x** section (for 2.0, from 1.x), which says what changes and in what order to do it.
- Any release that migrates a state file says so; see [The Outpost's state file](#the-outposts-state-file).

## Which versions work together

- **Before 1.0,** two versions work together only when they share the minor: `0.3.1` works with `0.3.4`, never with `0.4.0`. Upgrading to a new 0.x minor therefore means upgrading the Edge and every Outpost to it.
- **From 1.0,** an Edge accepts Outposts of its own major, whichever side is newer, and of the previous major. A 2.x Edge takes 2.x and 1.x Outposts, not 0.x.
- **An Outpost refuses only an Edge of an older major.** In a join token it refuses to join; once running it logs an error naming both versions and keeps running until the Edge is upgraded. It cannot know whether a newer major still accepts it, so it leaves that to the Edge.
- **On one host,** the `tnl` you type and the process it talks to must be the exact same version. Restart the process after upgrading it.
- **Builds with no release version** (`dev`) are never judged.

`tnl version` prints the version, the build and what it works with:

```
tnl 0.3.1 full
works with Edges and Outposts 0.3.x
```

## An Incompatible Outpost

The Edge's status commands name the state `incompatible`, and its log carries the `warn`. While it lasts:

- the Outpost's Routes answer 503 to Visitors
- its API calls are refused: "the Edge (0.3.4) doesn't accept this Outpost (0.4.0); upgrade the Edge". It says "upgrade the Outpost" when the Outpost is the older side
- the Edge logs one `warn` with both versions, again only when the Outpost's version changes

Upgrade the side the message names. The Outpost recovers within one ping, about 10 s after the versions are inside the window.

## A 0.2 Outpost under a 0.1.x Edge

A 0.1.x Edge cannot read a 0.2 Outpost's ping, which carries a field it lacks, so it drops it without an answer. It never learns the Outpost's version, so it does not mark it Incompatible. The Outpost, hearing no pongs, logs "No answer from the Edge" at `warn`, and `tnl status` shows the Tunnel down. It sends no Declaration, as the Declaration travels with the ping. Upgrade the Edge first, as above, and the Outpost recovers by itself. An Edge from 0.2 on judges any later Outpost by the version in its ping and marks it Incompatible, as described above.

## Joining with a token

A join token carries the Edge's version. An Outpost that cannot work with that Edge refuses the token before it writes anything, naming both versions and the Outpost version to install: the Edge's own. The install line `tnl outpost add` prints is pinned to the Edge's version, so it is always right.

## Refused requests

Both sides refuse fields they don't know, on the API, the tnl header, the ping, the Declaration, `TNL_*` variables and state files. A request an older Edge doesn't understand fails whole, with a message such as "the Edge (1.4.2) doesn't support `--new-opt`; it needs 1.5 or newer; upgrade the Edge". Nothing is changed.

## The Outpost's state file

A newer tnl migrates the Outpost's `state.json` when it starts, logs that at `info` and keeps the old file as `state.json.v<n>`, where `n` is the schema it had. An older tnl refuses a schema it doesn't know and leaves the file alone. To downgrade, stop the Outpost, put `state.json.v<n>` back as `state.json`, and start the older tnl.

## The Edge's state file

The Edge migrates its `state.json` the same way, and a restored backup too: a newer Edge migrates an older schema when it starts, logs that at `info` and keeps the old file as `state.json.v<n>`. Going from 0.1.x to 0.2 migrates schema 2 to 3:

1. Stop the Edge.
2. Back up the state directory (`tnl backup` while it runs, or a copy of the directory).
3. Start the new version. It migrates `state.json`.
4. Upgrade every Outpost.

To downgrade, stop the Edge, put `state.json.v2` back as `state.json`, and start the 0.1.x Edge. Changes made since the migration are lost.

**HSTS stays off on migrated Routes.** A 0.1.x Edge sent no `Strict-Transport-Security` header, and schema 3 added the per-Route `no_hsts`, so the migration sets it on every Route and every declared line. Turn HSTS on for a Route with `tnl route edit <hostname> --no-hsts=false` (see [Routes](routes.md#options)); a browser that has seen the header keeps it for a year. A declared Route is different: when its Outpost declares again, and its Declaration differs from the migrated one, which it does unless it also says `--no-hsts`, the Route takes the options of the Declaration and HSTS comes on. To keep HSTS off on a declared Route, add `--no-hsts` to its line when you upgrade the Outpost (a 0.1.x Outpost does not know the option).

## When the Edge cannot migrate its state

A `state.json` older than the migrations (schema 1, which no release wrote) is refused: "…it was written by an older tnl and can't be migrated before 1.0; back it up and start from an empty state directory". The way forward is to start over. Deleting only `state.json` is not enough: the Edge refuses again, naming the files it found beside it, such as `caddy/`. Empty the whole state directory.

1. Stop the Edge.
2. Back up the state directory. `tnl backup` does it while the Edge runs; a copy of the directory after the stop does too.
3. Empty the state directory, `caddy/` included. The Edge then orders its certificates again, one per Hostname or wildcard, which is well inside Let's Encrypt's limits.
4. Start the new version.
5. Upgrade every Outpost.

The state directory is `/var/lib/tnl` for both: the volume mounted there under Docker Compose, the directory itself under systemd. Under systemd, with the Edge stopped:

```sh
sudo systemctl stop tnl-edge
sudo cp -a /var/lib/tnl /root/tnl-state-backup
sudo find /var/lib/tnl -mindepth 1 -delete
# run the install line for the new version, then
sudo systemctl start tnl-edge
```

Under Compose, stop the container, copy the volume's contents out and empty it (a throwaway container that mounts the volume does both), then change the image tag and start it.

What comes back depends on how the Edge was set up:

- **A declared Edge** (`TNL_ZONE_*` and `TNL_OUTPOST_*`, with its key from `--key-file` or `TNL_KEY`) whose Outposts hold credentials and declare their Routes comes back by itself: the Zones and Outposts come from the settings, and each Outpost sends its Declaration again.
- **Otherwise,** add the Outposts again with `tnl outpost add`, which makes new tokens, and add their Routes again. Each Outpost joins with its new token.

Lost without a declaration: the Outposts, their Routes, the Zones and the Hostnames on hold.
