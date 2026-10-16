package caddyx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/hyprsh/tnl/internal/edge/conntrack"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/edge/routes"
	"github.com/hyprsh/tnl/internal/remind"
	"github.com/hyprsh/tnl/internal/wire"
)

// Dialer opens connections inside the Tunnel, normally through the Edge's
// netstack: func(ctx, a) (net.Conn, error) { return tnet.DialContextTCPAddrPort(ctx, a) }.
type Dialer func(ctx context.Context, addr netip.AddrPort) (net.Conn, error)

// Runtime is everything tnl's modules need that must outlive a Caddy reload: the
// Route table, where the Outposts are, the Tunnel, and the pool of idle Service
// connections. Caddy reprovisions its modules on every reload and cleans up the
// old ones while their streams still run, so none of this lives in a module.
// Modules find their Runtime by the Edge id in their JSON.
type Runtime struct {
	Table    routes.Table
	Outposts routes.Outposts
	Dialer   Dialer
	// Log receives Caddy's entries through the log core. Nil means slog.Default().
	Log *slog.Logger
	// Conns tracks every connection the transport opens. Nil tracks none.
	Conns *conntrack.Tracker
	// ServiceRoots are the roots an https:// Service's certificate is verified
	// against. Nil means the system's. The harness sets it; no flag does.
	ServiceRoots *x509.CertPool
	// Liveness says whether an Outpost is online. Nil treats every Outpost as
	// online.
	Liveness interface {
		State(outpost string) liveness.State
	}
	// Clock drives the certificate reminders; nil is the wall clock.
	Clock remind.Clock

	// Zones gives a wildcard Zone's DNS provider and current token, read when a
	// challenge runs, so a changed token needs no reload. The token lives in
	// the state and nowhere in Caddy's config (ADR 0005). Nil knows no Zone.
	Zones func(zone string) (provider, token string, ok bool)

	// AccessLog adds a request line for every successful request and for
	// whatever the Service answered (--access-log).
	AccessLog bool
	// InZone says whether a Hostname lies inside a Zone, which decides the level
	// of a 404's line. Nil treats every Hostname as inside one.
	InZone func(host string) bool

	pool pool
	reqs requests

	certsOnce sync.Once
	certs     *Certs
}

// Certs is the certificate manager, made on first use. It outlives reloads.
func (rt *Runtime) Certs() *Certs {
	rt.certsOnce.Do(func() {
		rt.certs = newCerts(func() *slog.Logger {
			if rt.Log != nil {
				return rt.Log
			}
			return slog.Default()
		}, rt.Clock)
	})
	return rt.certs
}

var (
	regMu    sync.RWMutex
	registry = map[string]*Runtime{}
)

// Register makes rt visible to the modules of the Caddy config built for id. It
// must happen before Start. The returned func unregisters it.
func Register(id string, rt *Runtime) (unregister func()) {
	regMu.Lock()
	defer regMu.Unlock()
	registry[id] = rt
	return func() {
		rt.Certs().close()
		regMu.Lock()
		defer regMu.Unlock()
		if registry[id] == rt {
			delete(registry, id)
		}
	}
}

func lookupRuntime(id string) (*Runtime, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	rt, ok := registry[id]
	if !ok {
		return nil, fmt.Errorf("tnl: no Edge runtime registered as %q", id)
	}
	return rt, nil
}

// tunnelWait is how long the transport waits for the Tunnel to an online
// Outpost (spec section 6, step 3). A variable so tests can shrink it.
var tunnelWait = 10 * time.Second

var errTunnelWait = errors.New("tnl: the Tunnel to the Outpost did not come up in time")

// poolIdleTimeout is how long an idle connection to a Service is kept.
const poolIdleTimeout = 90 * time.Second

// replyTimeout bounds the wait for the Outpost's reply to the tnl header. The
// Outpost's own limits are 10 s to read the header and 5 s to dial the Service.
const replyTimeout = 20 * time.Second

// pool holds one http.Transport per Hostname, so idle connections are pooled per
// Route. A Route that changes under the same Hostname gets a fresh transport and
// its predecessor's idle connections are dropped.
type pool struct {
	mu sync.Mutex
	m  map[string]*poolEntry
}

type poolEntry struct {
	route routes.Route
	addr  netip.AddrPort
	tr    *http.Transport
}

type ctxKey int

const requestIDKey ctxKey = 0

func (p *pool) transport(rt *Runtime, route routes.Route, addr netip.AddrPort) *http.Transport {
	key := routes.Normalize(route.Hostname)
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.m[key]; ok {
		if e.route == route && e.addr == addr {
			return e.tr
		}
		e.tr.CloseIdleConnections()
	}
	var protos http.Protocols
	protos.SetHTTP1(true)
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			id, _ := ctx.Value(requestIDKey).(string)
			return rt.dialOutpost(ctx, addr, route.Outpost, wire.Header{Service: route.Service, Hostname: route.Hostname, RequestID: id})
		},
		Protocols:           &protos,
		DisableCompression:  true,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     poolIdleTimeout,
		// No ResponseHeaderTimeout and no ExpectContinueTimeout: the Service may
		// take as long as it likes (spec section 6).
	}
	if route.TLS {
		// The transport handshakes over the connection DialContext returns. The
		// name is the Service URL's host, not the Visitor's Hostname.
		tr.TLSClientConfig = &tls.Config{
			ServerName: route.ServerName, RootCAs: rt.ServiceRoots,
			InsecureSkipVerify: route.InsecureSkipVerify,
		}
	}
	if p.m == nil {
		p.m = map[string]*poolEntry{}
	}
	p.m[key] = &poolEntry{route: route, addr: addr, tr: tr}
	return tr
}

func (p *pool) closeIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.m {
		e.tr.CloseIdleConnections()
	}
}

// ReplyError is a reply from the Outpost that isn't ok.
type ReplyError struct{ Reply wire.Reply }

func (e *ReplyError) Error() string {
	if e.Reply.Status == wire.StatusDialError {
		return "outpost could not dial the Service: " + e.Reply.Reason.String()
	}
	return "outpost " + e.Reply.Version + " refused the tnl header"
}

// dialOutpostOnce opens a Tunnel connection to the Outpost's data port, sends the tnl
// header and reads the reply. On ok the connection is ready for request bytes.
func (rt *Runtime) dialOutpostOnce(ctx context.Context, addr netip.AddrPort, outpost string, h wire.Header) (net.Conn, error) {
	// An online Outpost whose Tunnel isn't up (WireGuard has no session, so the
	// connect hangs) is waited for up to tunnelWait, then answered with 503.
	dctx, cancel := context.WithTimeout(ctx, tunnelWait)
	c, err := rt.Dialer(dctx, addr)
	waited := dctx.Err() == context.DeadlineExceeded && ctx.Err() == nil
	cancel()
	if err != nil {
		if waited {
			return nil, errTunnelWait
		}
		return nil, err
	}
	// Tracked from the dial on, so a close reaches connections still in the
	// header exchange too.
	c = rt.Conns.Track(h.Hostname, outpost, c)
	// Go's Transport lets a dial outlive the Visitor's request that started it,
	// because the connection may serve another request. So the wait does not end
	// with the Visitor; replyTimeout below is what bounds it. ctx ends it early
	// only when the Transport itself gives up on the dial.
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	c.SetDeadline(time.Now().Add(replyTimeout))
	if err := wire.WriteHeader(c, h); err != nil {
		c.Close()
		return nil, err
	}
	rp, err := wire.ReadReply(c)
	if err != nil {
		c.Close()
		return nil, err
	}
	if rp.Status != wire.StatusOK {
		c.Close()
		return nil, &ReplyError{Reply: rp}
	}
	if !stop() {
		c.Close()
		return nil, ctx.Err()
	}
	c.SetDeadline(time.Time{})
	return c, nil
}
