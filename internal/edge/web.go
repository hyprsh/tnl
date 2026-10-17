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

	"github.com/hyprsh/tnl/internal/api/server"
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
// once Caddy's side is built, after the server is. Without one (no web side)
// it waits for nothing.
type certsRef struct{ p atomic.Pointer[caddyx.Certs] }

func (r *certsRef) set(m *caddyx.Certs) { r.p.Store(m) }

// manager is the Caddy certificate manager, nil without a web side.
func (r *certsRef) manager() *caddyx.Certs { return r.p.Load() }

func (r *certsRef) Obtain(ctx context.Context, hostname string) error {
	if m := r.manager(); m != nil {
		return m.Obtain(ctx, hostname)
	}
	return nil
}

// Certificate and Covered are the `ls` replies' read of the manager. With no
// manager they know nothing.
func (r *certsRef) Certificate(name string) server.CertState {
	m := r.manager()
	if m == nil {
		return server.CertState{}
	}
	st := m.Report(name)
	return server.CertState{Managed: true, NotAfter: st.NotAfter, Failures: st.Failures, FirstFailure: st.FirstFailure, LastError: st.LastError}
}

func (r *certsRef) Covered(host string) bool {
	m := r.manager()
	return m != nil && m.Covered(host)
}

func (r *certsRef) Wait(ctx context.Context, name string) error {
	if m := r.manager(); m != nil {
		return m.Wait(ctx, name)
	}
	return nil
}

func (r *certsRef) WaitOwn(ctx context.Context, name string) error {
	if m := r.manager(); m != nil {
		return m.WaitOwn(ctx, name)
	}
	return nil
}

func (r *certsRef) Settle() {
	if m := r.manager(); m != nil {
		m.Settle()
	}
}

// drainTimeout is how long the Edge lets requests finish on SIGTERM (spec
// section 6).
const drainTimeout = 10 * time.Second

// webDeps is what the web side takes from the rest of the Edge.
type webDeps struct {
	StateDir     string
	WGPort       int
	Store        *state.Store
	Tunnel       *tunnel.Tunnel
	Conns        *conntrack.Tracker
	Live         *liveness.Liveness
	Certs        *certsRef
	TestDial     *lateTestDialer
	Wild         *wildcards
	DNSResolvers []string
	Log          *slog.Logger
}

// startWeb builds Caddy's config from the state and the flags, loads it, and
// keeps the router's table in step with the state. It returns the func that
// stops Caddy.
//
// The Routes are never Caddy config (ADR 0002): a change in the state swaps
// the table the router reads. Caddy loads here, and again when a wildcard Zone changes.
func startWeb(w *Web, d webDeps) (stop func(), err error) {
	table := new(routes.Mem)
	outposts := new(routes.MemOutposts)
	rt := &caddyx.Runtime{
		AccessLog: w.AccessLog, InZone: func(host string) bool { return d.Store.Snapshot().InZone(host) },
		Table: table, Outposts: outposts, Log: d.Log, Conns: d.Conns, ServiceRoots: w.ServiceRoots, Liveness: d.Live,
		Zones: func(zone string) (string, string, bool) {
			if z, ok := d.Store.Snapshot().Zone(zone); ok && z.Mode == state.Wildcard {
				return z.Provider, z.Token, true
			}
			return "", "", false
		},
		Dialer: func(ctx context.Context, a netip.AddrPort) (net.Conn, error) {
			return d.Tunnel.Net.DialContextTCPAddrPort(ctx, a)
		},
	}
	manager := rt.Certs()
	manager.SetStateDir(d.StateDir)
	publish := func(st *state.State) {
		known := make(map[string]netip.AddrPort, len(st.Outposts))
		for _, o := range st.Outposts {
			known[o.Name] = netip.AddrPortFrom(o.Address, wire.DataPort)
		}
		outposts.Swap(known)
		rs := make([]routes.Route, 0, len(st.Routes))
		for _, r := range st.Routes {
			svc := r.Parsed()
			var extra []routes.Header
			for _, h := range r.Headers {
				// The state holds only checked headers, so ok is always true.
				if name, value, ok := state.SplitHeader(h); ok {
					extra = append(extra, routes.Header{Name: name, Value: value})
				}
			}
			rs = append(rs, routes.Route{
				Hostname: r.Hostname, Outpost: r.Outpost, Service: svc.HostPort,
				TLS: svc.TLS, ServerName: svc.Host, InsecureSkipVerify: r.InsecureSkipVerify, HostHeader: r.HostHeader,
				NoHSTS: r.NoHSTS, Headers: extra,
			})
		}
		table.Swap(rs)
		// The wildcard Zones come first: a Hostname they cover gets no certificate
		// of its own.
		manager.SetWildcards(st.WildcardZones()...)
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
			d.Log.Error("could not hand Hostnames to Caddy for certificates", "error", err)
		}
	}
	publish(d.Store.Snapshot())
	d.Store.OnChange(publish)

	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	edgeID := hex.EncodeToString(id[:])
	unregister := caddyx.Register(edgeID, rt)
	d.Certs.set(manager)
	d.TestDial.set(rt)
	// WireGuard's UDP port and HTTP/3's are the same port when WireGuard is on
	// the HTTPS port, so HTTP/3 gives way (spec section 5, --wireguard-port).
	httpsPort := cmp.Or(w.HTTPSPort, 443)
	noHTTP3 := d.WGPort == httpsPort
	if noHTTP3 {
		d.Log.Info("HTTP/3 is off: WireGuard is on the HTTPS port over UDP", "port", httpsPort)
	}
	base := caddyx.Config{
		NoHTTP3: noHTTP3,
		EdgeID:  edgeID, StateDir: d.StateDir,
		HTTPListen: w.HTTPListen, HTTPSListen: w.HTTPSListen,
		HTTPPort: w.HTTPPort, HTTPSPort: w.HTTPSPort,
		ACME:                  &caddyx.ACME{CA: w.ACMECA, Email: w.ACMEEmail, TrustedRoots: w.ACMETrustedRoots},
		DNSPropagationTimeout: w.DNSPropagationTimeout,
		DNSResolvers:          d.DNSResolvers,
	}
	// A reload loads the same config with the wildcard Zones as they now stand.
	// Reloads run one at a time, and each loads the state as it is then.
	var reloadMu sync.Mutex
	reload := func() error {
		reloadMu.Lock()
		defer reloadMu.Unlock()
		c := base
		c.Wildcards = d.Store.Snapshot().WildcardZones()
		return caddyx.Start(c)
	}
	err = reload()
	if err != nil {
		unregister()
		return nil, fmt.Errorf("start the web side (Caddy): %w", err)
	}
	d.Wild.reload.set(reload)
	return func() {
		d.Wild.reload.clear()
		// Caddy's own Stop waits for nothing, so the drain is ours: it closes the
		// listeners and starts Caddy's shutdown, then Drain ends the streams, waits
		// for the other requests and closes what remains.
		if err := caddyx.Stop(); err != nil {
			d.Log.Warn("stopping Caddy", "error", err)
		}
		res := rt.Drain(cmp.Or(w.DrainTimeout, drainTimeout))
		d.Log.Info("web side stopped", "drained", res.Drained, "streams_cut", res.StreamsCut, "closed", res.Closed)
		unregister()
	}, nil
}
