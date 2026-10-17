# Monitoring

tnl needs nothing beyond its logs, `tnl status` and the exit code of `tnl status`. There is no metrics endpoint and no notification: your own monitoring runs the command and reads the code.

## `tnl status` on the Edge

```sh
sudo tnl status
```

It prints a summary, then what needs attention:

```
tnl 1.2.0 (full, abc1234)
Uptime:           1d
Address:          edge.example.com, WireGuard port 51820
Key:              SHA256:...
State directory:  /var/lib/tnl
Log:              level info, access log off
Outposts:         2 online, 1 offline
Routes:           5
Zones:            2
Certificates:     4, the soonest expires 2026-12-01

Needs attention
  - Outpost pi is offline, last seen 4m ago, 1 Route
```

or "Nothing needs attention". These are problems:

- an Offline or Incompatible Outpost, with when it was last seen and its number of Routes (an Outpost that is `not joined` is not a problem, and neither is one that is `never connected` during the 60 s after the Edge starts; after that it is Offline, "not heard from since the Edge started")
- a certificate whose renewal is failing, that failed, or that has expired, or that expires in under 7 days
- a declared line that waits, such as "newbox declares photos.example.com, which nas holds"

The exit code is the answer for a script:

| Code | Means |
|---|---|
| `0` | running, and nothing needs attention |
| `1` | running, but something needs attention |
| `2` | no running Edge or Outpost was found, or its socket can't be opened |

With several tnl processes on one host, say which with `--state-dir` or `TNL_STATE_DIR`.

## `tnl status` on an Outpost

```sh
tnl status
```

The Outpost's relay answers it itself, so it works while the Tunnel is down. It prints its own side and the Edge's:

```
Outpost nas, tnl 1.2.0 (outpost, abc1234)
Tunnel address:   100.64.0.2
State directory:  /var/lib/tnl
Allow:            192.168.1.0/24
Edge:             edge.example.com (203.0.113.7)
Tunnel:           up
Last answer:      4s ago
Edge version:     1.2.0

Routes
  media.example.com -> http://nas:8096

Declared
  TNL_ROUTE_MEDIA: media.example.com -> http://nas:8096

Nothing needs attention
```

While the Tunnel is down, `Tunnel:` reads `no answer for 4m` and the Routes line reads `Routes: unavailable while the Tunnel is down`, because the Routes are fetched from the Edge. A waiting declared line shows its reason, such as `(waiting: taken by newbox)`.

These are problems, and exit code 1:

- the Tunnel is down: the Edge has not answered for 30 s (a shorter silence, such as an Edge restart, is not)
- the Edge says this Outpost is Incompatible, or the Edge is too old for it
- a declared line is waiting

Exit code `2` is as on the Edge. Other commands on an Outpost whose Tunnel is down fail with "Outpost not connected"; `status` does not.

## `--json`

`tnl status --json`, on either role, prints the same facts for a script: RFC 3339 times, durations in seconds, and `attention` as a list of `{kind, subject, message}` that is empty when nothing needs attention. On an Outpost the reply has `role` `outpost`, and `edge` (with `state` `up` or `down`), `routes` (`null` while the Tunnel is down, with `routes_unavailable`) and `declared`. Check `role` first. The fields only ever grow within a major version. The text may change in any release; match on `kind` and `subject`, not on `message`.

## `tnl logs`

`tnl logs` prints the recent log lines of the running Edge or Outpost, wherever its stderr goes, so it works the same under systemd, Compose and a bare `tnl connect`:

```
tnl logs                      # what the process holds
tnl logs -f --level warn      # follow, warnings and errors only
tnl logs -n 50 --since 1h     # the last 50 lines from the past hour
tnl logs --json               # JSON lines
```

The process keeps its last 1000 lines or 1 MiB, whichever is smaller, in memory, at the level it logs to stderr (`--log-level`). A restart empties it, and lines older than that are in stderr or the journal only. The access log (`--access-log`) is not in it: it stays on stderr. Lines print in the format the process logs in, with a timestamp even under systemd. On an Outpost it shows that Outpost's own lines. It exits 2 when no Edge or Outpost is found, and `-f` ends with Ctrl-C. Like the rest of the logs, it is not an API.

## Which log lines to alert on

Logs are not an API: the wording can change in any release. These are the lines worth an alert today.

On the Edge:

| Line | Level | Means |
|---|---|---|
| `outpost offline` | warn | an Outpost has not been heard from for 60 s |
| `outpost is incompatible` | warn | an Outpost's version is outside the window |
| `certificate attempt failed` | warn | a certificate could not be obtained or renewed |
| `certificate still failing` | warn daily, error once expired | the failure goes on |
| `declared line waits`, `declared line still waits` | warn | a declared line cannot apply |

On an Outpost:

| Line | Level | Means |
|---|---|---|
| `No answer from the Edge`, `Still no answer from the Edge` | warn | the Tunnel is down |
| `the Edge says this Outpost is Incompatible` | warn | upgrade one side |
| `this Edge is too old for this Outpost` | error | the Edge is an older major; it keeps trying |
| `Route waiting`, `Route started waiting` | warn | a declared line waits |

`tnl status` covers the same problems with an exit code, so alert on that first and keep the log lines for the detail.

## Not in v1

A metrics endpoint, notifications and a Docker `HEALTHCHECK`. Run `tnl status` from your own checks.
