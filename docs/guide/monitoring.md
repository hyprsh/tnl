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

- an Offline or Incompatible Outpost, with when it was last seen and its number of Routes (an Outpost that is `not joined` is not a problem)
- a certificate whose renewal is failing, that failed, or that has expired, or that expires in under 7 days
- a declared line that waits, such as "newbox declares photos.example.com, which nas holds"

The exit code is the answer for a script:

| Code | Means |
|---|---|
| `0` | running, and nothing needs attention |
| `1` | running, but something needs attention |
| `2` | no running Edge or Outpost was found, or its socket can't be opened |

With several tnl processes on one host, say which with `--state-dir` or `TNL_STATE_DIR`.

## `--json`

`tnl status --json` prints the same facts for a script: RFC 3339 times, durations in seconds, and `attention` as a list of `{kind, subject, message}` that is empty when nothing needs attention. The fields only ever grow within a major version. The text may change in any release; match on `kind` and `subject`, not on `message`.
