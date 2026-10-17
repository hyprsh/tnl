# Troubleshooting

## What Visitors see

When the Edge can't pass a request on, it answers itself with one built-in page (or one line of plain text if the Visitor doesn't accept HTML). The page never names the Outpost, the Service's address, tnl or its version. The reason goes only to the log.

| Status | When | Message |
|---|---|---|
| 503 | the Outpost is offline or incompatible, or its Tunnel did not come up within 10 s | "app.example.com is offline" |
| 502 | the Service is unreachable | "app.example.com isn't answering" |
| 404 | no Route for the Hostname | "Nothing is served at app.example.com" |

Below the message, in small text, are the time and a short request ID. The same ID is in the Edge's log line for that request. Search for it to see why.

### Event streams

A request whose `Accept` includes `text/event-stream` never gets the 502 or 503 page. A browser's `EventSource` would take the page for a broken stream and not retry. The Edge instead resets the stream (HTTP/2 and HTTP/3) or closes the connection (HTTP/1.1), so the browser reconnects by itself. `curl` shows an empty reply or a reset, with no status. The log line still carries the status the Edge would have sent, with `reset=true`.

A WebSocket or event stream that is open when an Outpost goes offline, or when the Edge stops, is ended the same way.

If an Outpost stops or restarts and its closing packets are lost, the Edge still ends the streams through it within 15 s, because it probes idle Tunnel connections after 5 s and drops one after 3 unanswered probes 3 s apart. A restarted Outpost resets them at the first probe. An Outpost is only marked offline after 60 s, so a stream can end before the Outpost shows as offline.

## Reading the logs

Lines are `key=value`, one per event, at `info` by default. Under systemd:

```
journalctl -u tnl-edge            # everything
journalctl -u tnl-edge -p warning # only what needs attention
```

`--log-level debug` (`TNL_LOG_LEVEL`) adds retries, re-resolutions, connections and dial failures.

### Request lines

The Edge writes one line for every response it makes itself: a 502 or 503, a reset event stream, a request cut because the Outpost's connection dropped, and a 404 for a Hostname inside one of your Zones, all at `info`. A 404 for a Hostname outside every Zone, which is mostly scanners, is `debug`.

```
level=INFO msg=request id=3f2a9c1d5e7b8a40 visitor=203.0.113.9:51514 host=app.example.com method=GET path=/ proto=HTTP/2.0 status=502 duration=5.012s bytes_in=0 bytes_out=162 outpost=nas reason=refused
```

| Field | Meaning |
|---|---|
| `id` | the request ID, also on the error page. The Outpost's lines about the connection that carried the request have it too. A later request over the same kept-alive connection has an ID of its own that the Outpost's lines don't carry. |
| `visitor` | the Visitor's IP and port |
| `host`, `method`, `path`, `proto` | what was asked. `path` has no query string |
| `status` | what the Visitor got. `reset=true` follows it for a reset event stream, and `status` is then the one the Edge would have sent |
| `duration`, `bytes_in`, `bytes_out` | when the request ended, and what crossed in each direction |
| `outpost` | the Outpost the Route uses |
| `reason` | why, for a failure |

The reasons:

| `reason` | Meaning |
|---|---|
| `refused` | the Outpost reached the Service's address and nothing was listening, or the Service refused. The Edge retried for about 5 s first |
| `timeout` | the Outpost couldn't connect to the Service in 5 s. Not retried |
| `dns` | the Outpost couldn't resolve the Service's name |
| `allowlist` | the Outpost's allowlist doesn't permit the Service's address |
| `outpost offline` | nothing heard from the Outpost for 60 s, or its connection dropped under the request |
| `tunnel not up` | the Outpost was not yet considered offline, but its Tunnel did not come back within the 10 s a request waits. The request goes through as soon as the Tunnel returns within those 10 s |
| `outpost incompatible` | the Outpost's version is outside the window the Edge accepts |
| `no route` | no Route for the Hostname |

The Outpost logs each allowlist refusal too, with the same `id`.

### `--access-log`

Without it, a request that succeeds logs nothing, and neither does one the Service answered with its own error. `--access-log` (`TNL_ACCESS_LOG=true`) adds one line, at `info`, for each of those, with the same fields and no `reason`.

No line ever contains the query string, a header, a cookie, the User-Agent or the Referer.

### Lines that are not tnl's

- Caddy's own entries come through as tnl lines with `logger=...`. One "redirected default logger" line appears at every start.
- Run by hand with neither `XDG_CONFIG_HOME` nor `HOME` set, the Edge prints one JSON line from Caddy, `unable to determine directory for user configuration`, before its first tnl line. The systemd unit sets `XDG_CONFIG_HOME`, so it never appears there.
- certmagic's stale-lock messages go to raw stderr, and appear only after a crash.

## Stopping

On SIGTERM (`systemctl stop`, `docker stop`) the Edge:

1. stops accepting connections
2. ends every event stream and WebSocket at once
3. lets other requests finish for up to 10 s, moving on as soon as they are done
4. closes whatever remains, HTTP/3 included
5. logs `drained`, `streams_cut` and `closed` (requests still running after 10 s), and exits

So a deploy or restart costs a long upload nothing if it ends within 10 s, and cuts a longer one. Event streams reconnect on their own. The Edge unit sets `TimeoutStopSec=20s` and the Compose example `stop_grace_period: 20s`, so the supervisor waits longer than the drain.

An Outpost closes everything and exits at once.

## After an Edge restart

An Outpost keeps its credentials and reconnects by itself. It logs "No answer from the Edge" at `warn` only after 30 s of silence, and "Tunnel up" when the Edge is back. Visitors see the 503 page for an Outpost the Edge hasn't heard from in 60 s.
