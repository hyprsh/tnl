package edge

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/netip"

	"github.com/hyprsh/tnl/internal/edge/caddyx"
	"github.com/hyprsh/tnl/internal/edge/conntrack"
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
func startWeb(w *Web, stateDir string, wgPort int, store *state.Store, tun *tunnel.Tunnel, conns *conntrack.Tracker, log *slog.Logger) (stop func(), err error) {
	table := new(routes.Mem)
	outposts := new(routes.MemOutposts)
	publish := func(st *state.State) {
		for _, o := range st.Outposts {
			outposts.Set(o.Name, netip.AddrPortFrom(o.Address, wire.DataPort))
		}
		rs := make([]routes.Route, 0, len(st.Routes))
		for _, r := range st.Routes {
			rs = append(rs, routes.Route{Hostname: r.Hostname, Outpost: r.Outpost, Service: r.HostPort()})
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
		Table: table, Outposts: outposts, Log: log, Conns: conns,
		Dialer: func(ctx context.Context, a netip.AddrPort) (net.Conn, error) {
			return tun.Net.DialContextTCPAddrPort(ctx, a)
		},
	})
	// WireGuard's UDP port and HTTP/3's are the same port when WireGuard is on
	// the HTTPS port, so HTTP/3 gives way (spec section 5, --wireguard-port).
	httpsPort := cmp.Or(w.HTTPSPort, 443)
	noHTTP3 := wgPort == httpsPort
	if noHTTP3 {
		log.Info("HTTP/3 is off: WireGuard is on the HTTPS port over UDP", "port", httpsPort)
	}
	err = caddyx.Start(caddyx.Config{
		NoHTTP3: noHTTP3,
		EdgeID:  edgeID, StateDir: stateDir,
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
