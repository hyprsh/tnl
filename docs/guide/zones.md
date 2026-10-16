# Zones

A Zone is a domain the Edge serves Hostnames under. Add one on the Edge host before its first Route:

```
sudo tnl zone add example.com
sudo tnl zone ls
```

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
