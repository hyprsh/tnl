# Zones

A Zone is a domain the Edge serves Hostnames under. Add one on the Edge host before its first Route, or declare it in the Edge's settings ([Declaring the Edge](declaring-the-edge.md)), which refuses `zone add`, `edit` and `rm` while it does:

```
sudo tnl zone add example.com
sudo tnl zone ls
```

## Wildcard Zones

```
sudo tnl zone add example.com --wildcard cloudflare
```

The Zone gets one wildcard certificate through a DNS-01 challenge, so its Hostnames never appear in Certificate Transparency logs. The Cloudflare token needs Zone, Zone, Read and Zone, DNS, Edit on the DNS zone.

- The token is read with a hidden prompt on a terminal, and from stdin otherwise (`echo "$TOKEN" | sudo tnl zone add ...`). There is no flag and no variable for it.
- The Edge finds the DNS zone that holds the Zone by SOA lookup and prints it. If the token can't read that DNS zone it refuses the Zone and saves nothing.
- It then orders the certificates for `*.example.com` and `example.com`, waits up to about 60 s, and prints the names they cover. If they aren't ready by then it warns, and Caddy keeps trying.
- A Hostname two labels or more below the Zone, such as `a.b.example.com`, is not covered by `*.example.com`. Its Route gets a certificate of its own, and `route add` says its name will appear in Certificate Transparency logs.
- The token is kept in `state.json`, never in Caddy's config, a log line or an error message, and is deleted by `zone rm`.
- Adding or removing a wildcard Zone reloads Caddy. SSE streams survive it and WebSockets close.

## Nested Zones

Zones may nest. A Hostname belongs to the longest Zone that contains it, and a Zone's own name is in it. With the Zones `example.com` and `lab.example.com`, `app.lab.example.com` belongs to `lab.example.com` and `www.example.com` to `example.com`. A partial label doesn't count: `evilexample.com` is in neither.

## Removing a Zone

```
sudo tnl zone rm lab.example.com
sudo tnl zone rm example.com --with-routes
```

- Routes that a parent Zone still contains move to it. They keep serving, with the same certificate and no certificate error, and Caddy isn't reloaded.
- If removing the Zone would leave Routes outside every Zone, `zone rm` refuses, lists them and changes nothing. Remove those Routes first, or pass `--with-routes`.
- `--with-routes` removes those Routes, closes their open connections and stops renewing their certificates.
- A Route that an Outpost declares goes back to waiting instead of being lost: its line stays in the Outpost's Declaration, and it becomes a Route again when a Zone contains its Hostname. `zone add` does that for every waiting line it now contains, and says which; see [Declaring Routes](declaring-routes.md#lines-that-wait).

A Route's Hostname stops renewing whenever its Route goes, by `route rm`, `outpost rm --with-routes` or `zone rm --with-routes`. The certificate it holds keeps answering until it expires, and the Edge asks the CA for no new one.

`zone rm` runs on the Edge host only. An Outpost, and the Outpost-only build, refuse it.

## Switching a Zone's mode

```
sudo tnl zone edit example.com --wildcard cloudflare
sudo tnl zone edit example.com --per-hostname
sudo tnl zone edit example.com --token
```

`--wildcard` and `--token` read the token as `zone add` does: a hidden prompt on a terminal, stdin otherwise. Give exactly one of the three options. `zone edit` runs on the Edge host only.

Switching never shows a Visitor a certificate error. The old certificates keep serving until the new ones replace them.

- **To wildcard.** The Edge checks the token as `zone add` does and refuses it, changing nothing, if it can't read the DNS zone. Then it reloads Caddy and obtains `*.example.com` and `example.com`. The certificate each Route already has keeps answering, and is no longer renewed: the wildcard takes over when it expires. Routes added afterwards are served by the wildcard.
- **To per-Hostname.** The token is deleted from `state.json`. After the reload, each Route's Hostname gets a certificate of its own, and the command waits up to about 60 s for them. Until a Hostname has one, the dropped wildcard serves it, and it keeps doing so until it expires. If a certificate isn't there in time, the command warns and Caddy keeps trying.
- **`--token`** replaces the token of a wildcard Zone after the same check, and reloads nothing: open WebSockets stay open. A refused token leaves the old one in place.
- Switching to wildcard or back reloads Caddy, so SSE streams survive and WebSockets close.
- **Adding a nested wildcard Zone** moves the Routes it contains under the same rule. With `example.com` per-Hostname, `zone add lab.example.com --wildcard cloudflare` moves `app.lab.example.com` under the new wildcard without a certificate error.

## The Cloudflare token

Create it in the Cloudflare dashboard under My Profile, API Tokens, Create Token. It needs two permissions, limited to the DNS zone that holds the Zone (the one `zone add` prints):

- Zone, Zone, Read
- Zone, DNS, Edit

## Log lines that look stray

Two lines in the logs are not Edge messages and are expected:

- One `redirected default logger` line from Caddy at each start.
- certmagic's stale-lock messages, which go to raw stderr rather than the log. They appear only after a crash, while the lock files it left are cleared.

## What a Hostname costs in public

- A name that gets its own certificate appears in Certificate Transparency logs. A wildcard Zone keeps its Hostnames out, except for names two labels or more below it, which `route add` warns about.
- The DNS checks at `zone add` and `route add` only warn. They never refuse.
