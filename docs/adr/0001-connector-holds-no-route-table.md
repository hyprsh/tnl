# The Connector holds no Route table

Routes live only on the Edge. For each connection, the Edge sends the Service's `host:port` in a small, versioned tnl header over the Tunnel, and the Connector resolves that name on its own network, dials it and copies bytes. We chose this over a port per Route (Pangolin) and Host-header dispatch on the Connector (cloudflared), because both of those need the Route table pushed to the Connector and kept in sync. This also means overlapping LANs on different Connectors never collide, since the Edge never routes to a LAN address. It keeps the Connector a plain TCP forwarder that never parses HTTP. The cost is that the Connector dials whatever the Edge names; an optional allowlist kept on the Connector is the only limit an Edge can't change.

See [Data path: how a request travels from Edge to Service](https://github.com/hyprsh/tnl/issues/5).
