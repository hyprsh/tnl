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

On an Outpost's host the same commands work through the Outpost's own socket, with no `--outpost`: every Route goes through that Outpost, and you can change or remove only your own. See [Routes from an Outpost](routes-from-an-outpost.md).

```sh
tnl route add app.example.com http://localhost:3000
```

## Options

- **`--host-header VALUE`** replaces the Host header sent to the Service. Without it the Service sees the Visitor's Hostname. `route edit --host-header ""` clears it.
- **`--insecure-skip-verify`** skips verifying an `https://` Service's certificate (see below). `route edit --insecure-skip-verify=false` turns verification back on.

`route edit` changes any of `--service`, `--outpost`, `--host-header` and `--insecure-skip-verify`, and closes the Route's open connections so the next request meets the new Route, even when only an option changed.

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

The test dial is one attempt. A request that meets a refusing Service is retried for up to 5 s, but `route add` does not wait. If the Outpost is Offline or hasn't connected yet, the test dial is skipped.

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
