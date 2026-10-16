package caddyx

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/hyprsh/tnl/internal/edge/routes"
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

	pool pool
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
			return rt.dialOutpost(ctx, addr, wire.Header{Service: route.Service, Hostname: route.Hostname, RequestID: id})
		},
		Protocols:           &protos,
		DisableCompression:  true,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     poolIdleTimeout,
		// No ResponseHeaderTimeout and no ExpectContinueTimeout: the Service may
		// take as long as it likes (spec section 6).
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

// dialOutpost opens a Tunnel connection to the Outpost's data port, sends the tnl
// header and reads the reply. On ok the connection is ready for request bytes.
func (rt *Runtime) dialOutpost(ctx context.Context, addr netip.AddrPort, h wire.Header) (net.Conn, error) {
	c, err := rt.Dialer(ctx, addr)
	if err != nil {
		return nil, err
	}
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
