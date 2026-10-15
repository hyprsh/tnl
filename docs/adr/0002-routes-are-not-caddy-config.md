# Routes are not Caddy config

The Edge embeds Caddy, but Routes stay out of Caddy's config: a tnl Caddy module looks up each request's Hostname in tnl's own Route table, and on-demand TLS issues per-Hostname certificates only for Hostnames that have a Route. Every Caddy config load restarts all of Caddy and closes open WebSockets on every Route. `stream_close_delay` only postpones that, and `stream_detached` is unreleased. So Caddy's config holds only what rarely changes, and adding, removing or changing a Route never reloads it.

See [Data path: how a request travels from Edge to Service](https://github.com/hyprsh/tnl/issues/5).
