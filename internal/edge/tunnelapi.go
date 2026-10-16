package edge

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
	"github.com/hyprsh/tnl/internal/state"
	"github.com/hyprsh/tnl/internal/tunnel"
)

// serveTunnelAPI serves the API's handlers on 100.64.0.1:api.TunnelPort in the
// Tunnel's netstack, so nothing listens on a host address. The caller's actor
// is the Outpost whose Tunnel address the connection comes from. It stops when
// ctx ends and returns the channel the server's result arrives on.
func serveTunnelAPI(ctx context.Context, srv *server.Server, tun *tunnel.Tunnel, store *state.Store) (<-chan error, error) {
	ln, err := tun.Net.ListenTCPAddrPort(netip.AddrPortFrom(addr.Edge, api.TunnelPort))
	if err != nil {
		return nil, fmt.Errorf("listen on the Tunnel for the API: %w", err)
	}
	h := srv.HandlerBySource(func(remote string) (api.Actor, error) {
		ap, err := netip.ParseAddrPort(remote)
		if err != nil {
			return "", fmt.Errorf("no Outpost has the source address %q", remote)
		}
		for _, o := range store.Snapshot().Outposts {
			if o.Address == ap.Addr() {
				return api.OutpostActor(o.Name), nil
			}
		}
		return "", fmt.Errorf("no Outpost has the Tunnel address %s", ap.Addr())
	})
	done := make(chan error, 1)
	go func() { done <- srv.ServeHandler(ctx, ln, h) }()
	return done, nil
}
