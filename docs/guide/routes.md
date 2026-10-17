# Routes

A Route sends one Hostname to one Service through one Outpost. The Hostname is exact (no wildcards), must sit inside a Zone, and is held by at most one Route.

## Managing Routes

### From the Edge host

```sh
sudo tnl route add app.example.com http://localhost:3000 --outpost nas
sudo tnl route edit app.example.com --service http://localhost:3001
sudo tnl route rm app.example.com
tnl route ls
```

`route add` takes the Hostname, the Service URL and `--outpost NAME`.

### From an Outpost host

On an Outpost's host, `tnl route add`, `edit`, `rm` and `ls`, and `tnl zone ls`, work through the Outpost's own `tnl.sock`. The Outpost forwards them to the Edge, which takes the Outpost as the caller.

```sh
tnl route add app.example.com http://localhost:3000
tnl route ls
```

- There is no `--outpost`: every Route you add goes through this Outpost, and `--outpost` is refused.
- You can change or remove only Routes through this Outpost, and you can't move one onto another Outpost. A Hostname another Outpost holds is refused, naming the holder.
- If the Edge can't be reached, the command exits 1 with "Outpost not connected".
- The `tnl` you run must be the exact version of the running Outpost. If not, it refuses before sending anything and tells you to restart the Outpost's unit.
- The Outpost logs each Route change made through its CLI at `info`, and the Edge logs it with `actor=outpost:<name>`.

## Listing Routes

`tnl route ls` prints every Route, with a STATUS column that shows the first problem: `outpost not joined`, `outpost never connected`, `outpost offline` or `outpost incompatible`, then `no certificate` or `certificate expired`, then `renewal failing`, else `ok`.

`route ls` shows the options in the OPTIONS column, one word each: `host-header=app.lan`, `insecure-skip-verify=true`, `no-hsts=true` and `header="Name: value"` (quoted, because a value has spaces). In `--json`, `options` is a list of the same words without the quotes, such as `"header=Name: value"`.

A line an Outpost declares that is not a Route yet gets a row of its own, `waiting: taken by nas` or `waiting: outside every Zone`. While a Hostname moves between Outposts it shows two rows. An Outpost's `tnl route ls` prints the same as the Edge's. `--json` gives the stable form.

## Options

- **`--host-header VALUE`** replaces the Host header sent to the Service. Without it the Service sees the Visitor's Hostname. `route edit --host-header ""` clears it.
- **`--insecure-skip-verify`** skips verifying an `https://` Service's certificate (see below). `route edit --insecure-skip-verify=false` turns verification back on.
- **`--no-hsts`** leaves the `Strict-Transport-Security` header off every reply of this Route, including the Edge's own 502 and 503. `route edit --no-hsts=false` turns it back on. A browser that has already seen the header keeps it for a year, so use this from the start.
- **`--header 'Name: value'`**, repeatable, adds a response header to the Service's replies, such as `--header 'X-Frame-Options: DENY'`. The Service's own header of the same name wins, and the Edge's own replies (error pages, 502, 503) get none. `route edit --header` replaces the Route's whole list, `route edit --header ""` clears it, and a `route edit` without `--header` leaves it. A header name may be given once. tnl refuses names it manages or that would break HTTP: `Strict-Transport-Security` (use `--no-hsts`), `Via`, `Server`, `Content-Length`, `Content-Type`, `Transfer-Encoding` and the hop-by-hop headers (`Connection`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `Proxy-Connection`, `TE`, `Trailer`, `Upgrade`).

`route edit` changes any of `--service`, `--outpost`, `--host-header`, `--insecure-skip-verify`, `--no-hsts` and `--header`, and closes the Route's open connections so the next request meets the new Route, even when only an option changed.

## `https://` Services

The Service URL is `http://` or `https://`, a host and an optional port (80 or 443 by default), and no path. For an `https://` Service the Edge speaks TLS to the Service itself, through the Outpost, which only copies bytes.

The Edge verifies the Service's certificate against the host in the URL. A Service with a self-signed or private-CA certificate fails that check and Visitors get a 502. Pass `--insecure-skip-verify` to accept any certificate, for that Route only.

## The test dial and its warnings

After saving, `route add` test-dials the Service the way a request would: through the Outpost, and for a verified `https://` Service through the TLS handshake. `route edit` does the same when the Service changed, and does nothing extra when only an option changed.

The Route is saved either way. A problem is a warning, printed to stderr, with exit status 0, and the Edge logs it at `warn`:

| Warning | Meaning |
|---|---|
| the Outpost's allowlist refuses the Service | The Outpost runs with `--allow` and doesn't list this Service. Visitors get a 502 until the Outpost allows it. |
| the Service doesn't answer (`refused`, `timeout` or `dns`) | Nothing accepted the connection, it took over 5 s, or the Outpost couldn't resolve the host. |
| the certificate fails verification | Pass `--insecure-skip-verify` if the Service's certificate is self-signed or from a private CA. |
| the TLS handshake failed | The Service didn't speak TLS, or refused the handshake. Check the URL's scheme. |

The test dial is one attempt. A request that meets a refusing Service is retried for up to 5 s, but `route add` does not wait. If the Outpost is Offline or hasn't connected yet, there is no test dial, and a warning says so.

## The allowlist

By default an Outpost dials whatever `host:port` the Edge names. `tnl connect --allow` (or `TNL_ALLOW`) limits that to a comma-separated list:

```sh
tnl connect --allow 192.168.1.0/24,localhost:3000
```

- **A CIDR** matches the address the Service's host resolves to, on any port. A literal IP in the URL needs no lookup.
- **A `host:port`** matches the name and port exactly as the Edge sent them, which is the Service URL in lower case, with the default port filled in: `http://localhost:3000` is `localhost:3000`.
- A bare IP is not an entry; write `192.168.1.5/32`.
- A malformed list makes `tnl connect` exit with status 78.

A Service the list refuses is answered with the `allowlist` dial error: Visitors get a 502, and the request is not retried. The Outpost logs each refusal at `info`, with the request ID and the address the Edge named, and logs its allowlist when it starts (`allow=none` when it has none).

## Headers the Edge sets

The Edge passes the Service's reply through, with these changes:

- **`Strict-Transport-Security: max-age=31536000`** is added to every HTTPS reply, unless the Service sends its own, which is left as it is, or the Route has `--no-hsts`. There is no `includeSubDomains` or `preload`. The port-80 redirect sends none.
- **`Via`** is removed from replies to Visitors, so they never see `Caddy`.
- **`Server`** is the Service's own. If the Service sends none, the reply has none.

The Edge's own replies (its error pages, the `200` it gives an event-stream request when the Service is down, and the redirect from port 80) carry no `Server` header and `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'`.

Everything else is the Service's to set, or the Route's `--header`: `Content-Security-Policy`, `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy` and `Permissions-Policy`. The Edge doesn't add them to the Service's replies by itself, because it can't know what the app needs. A Route can add them for a Service that can't, with `--header 'Permissions-Policy: geolocation=()'`; where the Service sends the same header, the Service's wins. In nginx, for example:

```nginx
add_header Content-Security-Policy "default-src 'self'" always;
add_header X-Content-Type-Options nosniff always;
add_header Referrer-Policy strict-origin-when-cross-origin always;
add_header Permissions-Policy "geolocation=()" always;
```

Findings from scanners about cookies, privacy notices or scraping belong to the site, not to the proxy.

## Declaring Routes on the Outpost

An Outpost can list its own Routes in `TNL_ROUTE_*` variables or `--route` flags instead of using `tnl route add`. While it does, those Routes can't be changed with the CLI. See [Declaring Routes on the Outpost](declaring-routes.md).
