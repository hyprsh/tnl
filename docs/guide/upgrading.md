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
- **An Outpost refuses only an Edge of an older major.** It cannot know whether a newer major still accepts it, so it leaves that to the Edge.
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

## Joining with a token

A join token carries the Edge's version. An Outpost that cannot work with that Edge refuses the token before it writes anything, naming both versions and the Outpost version to install: the Edge's own. The install line `tnl outpost add` prints is pinned to the Edge's version, so it is always right.

## Refused requests

Both sides refuse fields they don't know, on the API, the tnl header, the ping, the Declaration, `TNL_*` variables and state files. A request an older Edge doesn't understand fails whole, with a message such as "the Edge (1.4.2) doesn't support `--new-opt`; it needs 1.5 or newer; upgrade the Edge". Nothing is changed.

## The Outpost's state file

A newer tnl migrates the Outpost's `state.json` when it starts, logs that at `info` and keeps the old file as `state.json.v<n>`, where `n` is the schema it had. An older tnl refuses a schema it doesn't know and leaves the file alone. To downgrade, stop the Outpost, put `state.json.v<n>` back as `state.json`, and start the older tnl.

The Edge's `state.json` is not migrated before 1.0: a tnl refuses an Edge state, or a backup, whose schema it doesn't know.
