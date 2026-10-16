package edge

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hyprsh/tnl/internal/edge/caddyx"
	"github.com/hyprsh/tnl/internal/edge/conntrack"
	"github.com/hyprsh/tnl/internal/edge/liveness"
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
	// ServiceRoots are the roots an https:// Service is verified against; nil
	// means the system's. Only the harness sets it.
	ServiceRoots *x509.CertPool
	// AccessLog adds request lines for successful requests and for what the
	// Service answered (--access-log, TNL_ACCESS_LOG).
	AccessLog bool
	// DrainTimeout is how long stopping lets in-flight requests finish; zero
	// is the spec's 10 s. The harness shrinks it.
	DrainTimeout time.Duration
	// DNSPropagationTimeout is how long a DNS-01 challenge waits for its TXT
	// record to show in DNS; zero is Caddy's 2 min and -1 skips the check. The
	// harness skips it, as its records are not in DNS.
	DNSPropagationTimeout time.Duration
}

// certsRef gives the API server the certificate manager, which only exists
// once Caddy's side is built, after the server is. Without Caddy it waits for
// nothing.
type certsRef struct{ p atomic.Pointer[caddyx.Certs] }

// Obtain implements server.Certificates.
func (r *certsRef) Obtain(ctx context.Context, hostname string) error {
	if c := r.p.Load(); c != nil {
		return c.Obtain(ctx, hostname)
	}
	return nil
}

// Wait implements the certificate wait of server.Wildcards.
func (r *certsRef) Wait(ctx context.Context, name string) error {
	if c := r.p.Load(); c != nil {
		return c.Wait(ctx, name)
	}
	return nil
}

// drainTimeout is how long the Edge lets requests finish on SIGTERM (spec
// section 6).
const drainTimeout = 10 * time.Second

// startWeb builds Caddy's config from the state and the flags, loads it, and
// keeps the router's table in step with the state. It returns the func that
// stops Caddy.
//
// The Routes are never Caddy config (ADR 0002): a change in the state swaps
// the table the router reads, and Caddy is loaded once, here.
func startWeb(w *Web, stateDir string, wgPort int, store *state.Store, tun *tunnel.Tunnel, conns *conntrack.Tracker, live *liveness.Liveness, certs *certsRef, testDial *lateTestDialer, wild *wildcards, dnsResolvers []string, log *slog.Logger) (stop func(), err error) {
	table := new(routes.Mem)
	outposts := new(routes.MemOutposts)
	rt := &caddyx.Runtime{
		AccessLog: w.AccessLog, InZone: func(host string) bool { return store.Snapshot().InZone(host) },
		Table: table, Outposts: outposts, Log: log, Conns: conns, ServiceRoots: w.ServiceRoots, Liveness: live,
		Zones: func(zone string) (string, string, bool) {
			for _, z := range store.Snapshot().Zones {
				if z.Name == zone && z.Mode == state.Wildcard {
					return z.Provider, z.Token, true
				}
			}
			return "", "", false
		},
		Dialer: func(ctx context.Context, a netip.AddrPort) (net.Conn, error) {
			return tun.Net.DialContextTCPAddrPort(ctx, a)
		},
	}
	manager := rt.Certs()
	publish := func(st *state.State) {
		known := make(map[string]netip.AddrPort, len(st.Outposts))
		for _, o := range st.Outposts {
			known[o.Name] = netip.AddrPortFrom(o.Address, wire.DataPort)
		}
		outposts.Swap(known)
		rs := make([]routes.Route, 0, len(st.Routes))
		for _, r := range st.Routes {
			svc := r.Parsed()
			rs = append(rs, routes.Route{
				Hostname: r.Hostname, Outpost: r.Outpost, Service: svc.HostPort,
				TLS: svc.TLS, ServerName: svc.Host, InsecureSkipVerify: r.InsecureSkipVerify, HostHeader: r.HostHeader,
			})
		}
		table.Swap(rs)
		// The wildcard Zones come first: a Hostname they cover gets no certificate
		// of its own.
		manager.SetWildcards(wildcardZones(st)...)
		// Every Route Hostname goes to Caddy's TLS app, now and for every new
		// Route, and a Hostname whose Route is gone, by whatever command,
		// stops renewing. Retain only queues work, so it is safe under the
		// store's lock. The Zone that holds a Hostname is the one shared
		// question; a Hostname in none gets no certificate.
		names := make([]string, 0, len(rs))
		for _, r := range rs {
			if _, in := st.ZoneOf(r.Hostname); in {
				names = append(names, r.Hostname)
			}
		}
		if err := manager.Retain(names...); err != nil {
			log.Error("could not hand Hostnames to Caddy for certificates", "error", err)
		}
	}
	publish(store.Snapshot())
	store.OnChange(publish)

	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	edgeID := hex.EncodeToString(id[:])
	unregister := caddyx.Register(edgeID, rt)
	certs.p.Store(manager)
	testDial.set(rt)
	// WireGuard's UDP port and HTTP/3's are the same port when WireGuard is on
	// the HTTPS port, so HTTP/3 gives way (spec section 5, --wireguard-port).
	httpsPort := cmp.Or(w.HTTPSPort, 443)
	noHTTP3 := wgPort == httpsPort
	if noHTTP3 {
		log.Info("HTTP/3 is off: WireGuard is on the HTTPS port over UDP", "port", httpsPort)
	}
	base := caddyx.Config{
		NoHTTP3: noHTTP3,
		EdgeID:  edgeID, StateDir: stateDir,
		HTTPListen: w.HTTPListen, HTTPSListen: w.HTTPSListen,
		HTTPPort: w.HTTPPort, HTTPSPort: w.HTTPSPort,
		ACME:                  &caddyx.ACME{CA: w.ACMECA, Email: w.ACMEEmail, TrustedRoots: w.ACMETrustedRoots},
		DNSPropagationTimeout: w.DNSPropagationTimeout,
		DNSResolvers:          dnsResolvers,
	}
	// A reload loads the same config with the wildcard Zones as they now stand.
	// Reloads run one at a time, and each loads the state as it is then.
	var reloadMu sync.Mutex
	reload := func() error {
		reloadMu.Lock()
		defer reloadMu.Unlock()
		c := base
		c.Wildcards = wildcardZones(store.Snapshot())
		return caddyx.Start(c)
	}
	err = reload()
	if err != nil {
		unregister()
		return nil, fmt.Errorf("start the web side (Caddy): %w", err)
	}
	wild.reload.Store(&reload)
	return func() {
		wild.reload.Store(nil)
		// Caddy's own Stop waits for nothing, so the drain is ours: it closes the
		// listeners and starts Caddy's shutdown, then Drain ends the streams, waits
		// for the other requests and closes what remains.
		if err := caddyx.Stop(); err != nil {
			log.Warn("stopping Caddy", "error", err)
		}
		res := rt.Drain(cmp.Or(w.DrainTimeout, drainTimeout))
		log.Info("web side stopped", "drained", res.Drained, "streams_cut", res.StreamsCut, "closed", res.Closed)
		unregister()
	}, nil
}
