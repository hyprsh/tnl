# Zones

A Zone is a domain the Edge serves Hostnames under. Add one on the Edge host before its first Route:

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

A Route's Hostname stops renewing whenever its Route goes, by `route rm`, `outpost rm --with-routes` or `zone rm --with-routes`. The certificate it holds keeps answering until it expires, and the Edge asks the CA for no new one.

`zone rm` runs on the Edge host only. An Outpost, and the Outpost-only build, refuse it.
