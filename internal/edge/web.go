package edge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/netip"

	"github.com/hyprsh/tnl/internal/edge/caddyx"
	"github.com/hyprsh/tnl/internal/edge/routes"
	"github.com/hyprsh/tnl/internal/state"
	"github.com/hyprsh/tnl/internal/tunnel"
	"github.com/hyprsh/tnl/internal/wire"
)

// Web is the Caddy side of the Edge: where it listens and which ACME CA it gets
// certificates from. A nil Web in Config starts no Caddy, which is what the
// tests of the Tunnel and the API want.
type Web struct {
	// HTTPListen and HTTPSListen default to ":80" and ":443". HTTPPort and
	// HTTPSPort are the ports Visitors reach, defaulting to 80 and 443.
	HTTPListen, HTTPSListen string
	HTTPPort, HTTPSPort     int
	// ACMECA is the ACME directory URL; empty is Let's Encrypt production.
	ACMECA    string
	ACMEEmail string
	// ACMETrustedRoots are PEM files of roots to trust for the CA's own TLS.
	ACMETrustedRoots []string
}

// startWeb builds Caddy's config from the state and the flags, loads it, and
// keeps the router's table in step with the state. It returns the func that
// stops Caddy.
//
// The Routes are never Caddy config (ADR 0002): a change in the state swaps
// the table the router reads, and Caddy is loaded once, here.
func startWeb(w *Web, stateDir string, store *state.Store, tun *tunnel.Tunnel, log *slog.Logger) (stop func(), err error) {
	table := new(routes.Mem)
	outposts := new(routes.MemOutposts)
	publish := func(st *state.State) {
		for _, o := range st.Outposts {
			outposts.Set(o.Name, netip.AddrPortFrom(o.Address, wire.DataPort))
		}
		rs := make([]routes.Route, 0, len(st.Routes))
		for _, r := range st.Routes {
			_, hostPort, err := state.ParseService(r.Service)
			if err != nil {
				log.Error("skipping a Route whose Service URL this tnl cannot serve", "hostname", r.Hostname, "service", r.Service, "error", err)
				continue
			}
			rs = append(rs, routes.Route{Hostname: r.Hostname, Outpost: r.Outpost, Service: hostPort})
		}
		table.Swap(rs)
	}
	publish(store.Snapshot())
	store.OnChange(publish)

	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	edgeID := hex.EncodeToString(id[:])
	unregister := caddyx.Register(edgeID, &caddyx.Runtime{
		Table: table, Outposts: outposts,
		Dialer: func(ctx context.Context, a netip.AddrPort) (net.Conn, error) {
			return tun.Net.DialContextTCPAddrPort(ctx, a)
		},
	})
	err = caddyx.Start(caddyx.Config{
		EdgeID: edgeID, StateDir: stateDir,
		HTTPListen: w.HTTPListen, HTTPSListen: w.HTTPSListen,
		HTTPPort: w.HTTPPort, HTTPSPort: w.HTTPSPort,
		ACME: &caddyx.ACME{CA: w.ACMECA, Email: w.ACMEEmail, TrustedRoots: w.ACMETrustedRoots},
	})
	if err != nil {
		unregister()
		return nil, fmt.Errorf("start the web side (Caddy): %w", err)
	}
	return func() {
		if err := caddyx.Stop(); err != nil {
			log.Warn("stopping Caddy", "error", err)
		}
		unregister()
	}, nil
}
