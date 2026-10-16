# Declaring Routes on the Outpost

An Outpost can list its own Routes, so that starting it is all the setup its Services need. `tnl connect` takes one line per Route, as `TNL_ROUTE_<LABEL>` variables or repeated `--route` flags, never both:

```sh
TNL_ROUTE_MEDIA="media.example.com http://localhost:8096 --host-header jellyfin --insecure-skip-verify" tnl connect
tnl connect --route "media.example.com http://localhost:8096"
```

In Compose, the Outpost lists them next to its token or credential ([`examples/compose/outpost.yaml`](../../examples/compose/outpost.yaml) shows the credential; [Declaring the Edge](declaring-the-edge.md) explains it):

```yaml
environment:
  TNL_TOKEN: paste-the-token-here
  TNL_ROUTE_PHOTOS: "photos.example.com http://immich:2283"
  TNL_ROUTE_MEDIA: "media.example.com http://jellyfin:8096 --host-header jellyfin"
```

## The lines

- `<LABEL>` is letters, digits and `_`. It names the line in messages and never reaches the Edge.
- Words are split on whitespace, with no quoting. The Hostname and Service URL come first, then the options.
- The lines are checked when `tnl connect` starts. A syntax error, an unknown option, a Service URL with a path or a scheme other than `http` or `https`, an invalid Hostname, one Hostname on two lines, or `--route` together with `TNL_ROUTE_*` makes it exit with status 78. The message names the variable: `TNL_ROUTE_MEDIA: unknown option --host-heder`.
- `tnl connect` and `tnl edge` also refuse any other `TNL_*` variable they don't know, with a "did you mean" (`unknown variable TNL_ADRESS: did you mean TNL_ADDRESS?`) that echoes names, never values.

## How the Edge applies them

- The Outpost sends its lines to the Edge, and sends them again only when they differ from the Edge's copy. Restart it after changing a line: a changed line edits its Route, a line you removed removes its Route, and a line that didn't change keeps its Route and its open connections.
- The Edge test-dials new lines and lines whose Service changed. A Service that refuses connections doesn't stop the line: the Route is made, and the Outpost logs a `warn`.
- While an Outpost declares, it is the only source of its Routes. `tnl route add`, `edit` and `rm` are refused for them, on the Edge host and on the Outpost: `nas declares its Routes; change TNL_ROUTE_* on nas and restart it`. That includes moving a Route onto or off it.
- An Outpost that had Routes made with `tnl route add` loses them when it starts declaring, and each removal is logged at `warn` on both sides. Starting it with no lines hands it back: its declared Routes go, and `tnl route add` works for it again.
- `tnl outpost rm` removes the Declaration with the Outpost, and `tnl outpost rekey` keeps it.

## Lines that wait

A line whose Hostname another Outpost holds, or that is outside every Zone, waits while every other line applies. Nothing needs restarting: the line becomes a Route by itself, in the same change that frees its Hostname or adds a Zone for it.

- **A Hostname is freed** by `tnl route rm`, `tnl outpost rm --with-routes`, or another Outpost's Declaration that no longer has the line. The command that freed it says who has it now: `newbox declared photos.example.com and now serves it`.
- **A Zone is added** with `tnl zone add`, and the lines inside it become Routes.
- **`tnl zone rm --with-routes`** sends the declared Routes inside the Zone back to waiting, and removes the Routes made with the CLI. The command lists the declared ones.
- **If several Outposts wait for one Hostname,** the one that declared it first gets it. A line keeps the time it was first declared for as long as it stays in the Declaration.
- No `tnl route add` can take the Hostname in between: the Edge hands it over in the same locked change that frees it.

Both sides log it. The Edge logs a `warn` when a line starts waiting, with the Outpost, Hostname and reason, and reminds after 5 minutes, after 30 minutes and then hourly while it waits. It logs an `info` when the line applies. The Outpost logs a `warn` for each waiting line, naming its variable (`variable=TNL_ROUTE_PHOTOS`), when it starts and after each send, and an `info` ("Route now applies") when the Edge's answer shows the line applying.

## Moving a Service to another Outpost

To move `photos.example.com` from `nas` to `newbox`, do it in either order:

1. Declare the line on `newbox` and start it. The line waits, and `nas` keeps serving.
2. Remove the line from `nas` and restart `nas`. `newbox` serves `photos.example.com` from that moment.

Or remove it from `nas` first and declare it on `newbox` afterwards; `photos.example.com` is unserved in between. Moving a Route with `tnl route edit --outpost` doesn't free the Hostname: it stays held, now through the other Outpost, and nothing else takes it.
