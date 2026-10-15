# The Outpost holds no Route table

Routes live only on the Edge. For each connection, the Edge sends the Service's `host:port` in a small, versioned tnl header over the Tunnel, and the Outpost resolves that name on its own network, dials it and copies bytes. We chose this over a port per Route (Pangolin) and Host-header dispatch on the Outpost (cloudflared), because both of those need the Route table pushed to the Outpost and kept in sync. This also means overlapping LANs on different Outposts never collide, since the Edge never routes to a LAN address. It keeps the Outpost a plain TCP forwarder that never parses HTTP. The cost is that the Outpost dials whatever the Edge names; an optional allowlist kept on the Outpost is the only limit an Edge can't change.

See [Data path: how a request travels from Edge to Service](https://github.com/hyprsh/tnl/issues/5).
