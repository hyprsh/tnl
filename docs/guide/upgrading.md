# Upgrading

Upgrade the Edge first, then each Outpost whenever you reach its machine. The Edge judges every Outpost by its version, and an Outpost outside the Edge's window is **Incompatible**.

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
