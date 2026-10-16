package caddyx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddytls"

	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/edge/routes"
)

func init() {
	caddy.RegisterModule(Router{})
	caddy.RegisterModule(Transport{})
	caddy.RegisterModule(Permission{})
	caddy.RegisterModule(EventHandler{})
}

const (
	varRoute     = "tnl.route"
	varRequestID = "tnl.request_id"
)

// Router is the tnl_router HTTP handler. It looks the request's Hostname up in
// the Route table on every request, answers a bare 404 if there is no Route, and
// otherwise hands the request to the next handler (Caddy's reverse_proxy) with
// the Route attached.
type Router struct {
	// Edge names the Runtime this router reads.
	Edge string `json:"edge"`

	rt *Runtime
}

// CaddyModule implements caddy.Module.
func (Router) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "http.handlers.tnl_router", New: func() caddy.Module { return new(Router) }}
}

// Provision implements caddy.Provisioner.
func (r *Router) Provision(caddy.Context) (err error) {
	r.rt, err = lookupRuntime(r.Edge)
	return err
}

// ServeHTTP implements caddyhttp.MiddlewareHandler. It registers the request
// for the drain, counts its bytes, and writes its request line when it ends.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request, next caddyhttp.Handler) (err error) {
	started := time.Now()
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)
	id := newRequestID()
	caddyhttp.SetVar(ctx, varRequestID, id)
	a := r.rt.begin(cancel, isEventStream(req) || isUpgrade(req))
	defer r.rt.end(a)
	caddyhttp.SetVar(ctx, varActive, a)

	rec := &recorder{ResponseWriter: w}
	var body *countBody
	if req.Body != nil && req.Body != http.NoBody {
		body = &countBody{ReadCloser: req.Body}
		req.Body = body
	}
	host := dnsname.HostOnly(req.Host)
	route, ok := r.rt.Table.Lookup(host)
	line := requestLog{
		req: req, id: id, rec: rec, body: body, started: started, a: a,
		inZone: r.rt.InZone == nil || r.rt.InZone(host), outpost: route.Outpost,
		vars: func(k string) any { return caddyhttp.GetVar(ctx, k) },
	}
	defer func() {
		p := recover()
		if p != nil {
			line.failed = http.ErrAbortHandler
		} else {
			line.failed = err
		}
		r.rt.writeRequestLine(line)
		if p != nil {
			panic(p)
		}
	}()
	if !ok {
		r.rt.answer(rec, req, id, http.StatusNotFound, "no route", false)
		return nil
	}
	caddyhttp.SetVar(ctx, varRoute, route)
	err = next.ServeHTTP(rec, req)
	if errors.Is(err, errResetStream) || a.wasCut() {
		// The Transport asked for the stream to be reset (HTTP/2, HTTP/3) or
		// the connection closed (HTTP/1.1), or the drain cut the request.
		// Aborting the handler does both.
		panic(http.ErrAbortHandler)
	}
	return err
}

func isUpgrade(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Upgrade"), "websocket")
}

func newRequestID() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Transport is the http.reverse_proxy.transport.tnl module. It dials the Route's
// Outpost through the Tunnel, writes the tnl header, reads the reply, and then
// speaks HTTP/1.1 to the Service over that connection. It is its own
// http.RoundTripper and keeps no state: the pools live in the Runtime.
type Transport struct {
	// Edge names the Runtime this transport uses.
	Edge string `json:"edge"`

	rt *Runtime
}

// CaddyModule implements caddy.Module.
func (Transport) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "http.reverse_proxy.transport.tnl", New: func() caddy.Module { return new(Transport) }}
}

// Provision implements caddy.Provisioner.
func (t *Transport) Provision(caddy.Context) (err error) {
	t.rt, err = lookupRuntime(t.Edge)
	return err
}

// Cleanup runs when a reload unloads the config this module was in, while
// requests of that config may still be streaming. It drops idle connections
// only.
func (t *Transport) Cleanup() error {
	if t.rt != nil {
		t.rt.pool.closeIdle()
	}
	return nil
}

// RoundTrip implements http.RoundTripper. A Service the Edge can't reach gets a
// 502 page, or a reset if the request is for an event stream (ADR 0007).
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	route, ok := caddyhttp.GetVar(ctx, varRoute).(routes.Route)
	if !ok {
		return nil, errors.New("tnl: request reached the transport without a Route")
	}
	id, _ := caddyhttp.GetVar(ctx, varRequestID).(string)
	// Step 3 of a request: an Offline Outpost gets 503 at once. One the Edge has
	// not heard from yet (it joined a moment ago) is waited for like an online one.
	if t.rt.Liveness != nil && t.rt.Liveness.State(route.Outpost) == liveness.Offline {
		return t.rt.fail(req, id, http.StatusServiceUnavailable, "outpost offline")
	}
	addr, ok := t.rt.Outposts.Addr(route.Outpost)
	if !ok {
		return t.rt.fail(req, id, http.StatusServiceUnavailable, "outpost offline")
	}
	out := req.Clone(context.WithValue(ctx, requestIDKey, id))
	out.URL.Scheme = "http"
	if route.TLS {
		out.URL.Scheme = "https"
	}
	out.URL.Host = route.Hostname
	if route.HostHeader != "" {
		out.Host = route.HostHeader
	}

	resp, err := t.rt.pool.transport(t.rt, route, addr).RoundTrip(out)
	if err != nil {
		if errors.Is(err, errTunnelWait) {
			return t.rt.fail(req, id, http.StatusServiceUnavailable, "outpost offline")
		}
		var re *ReplyError
		if errors.As(err, &re) {
			return t.rt.fail(req, id, http.StatusBadGateway, replyReason(re))
		}
		return nil, err
	}
	if a, ok := caddyhttp.GetVar(ctx, varActive).(*active); ok {
		a.attach(resp)
	}
	return resp, nil
}

// Permission is the tls.permission.tnl module. Caddy asks it before it issues a
// certificate on demand, and it allows only Hostnames that have a Route, so a
// scanner probing random SNI names gets nothing.
type Permission struct {
	// Edge names the Runtime whose Route table decides.
	Edge string `json:"edge"`

	rt *Runtime
}

// CaddyModule implements caddy.Module.
func (Permission) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "tls.permission.tnl", New: func() caddy.Module { return new(Permission) }}
}

// Provision implements caddy.Provisioner.
func (p *Permission) Provision(caddy.Context) (err error) {
	p.rt, err = lookupRuntime(p.Edge)
	return err
}

// CertificateAllowed implements caddytls.OnDemandPermission.
func (p *Permission) CertificateAllowed(_ context.Context, name string) error {
	if _, ok := p.rt.Table.Lookup(name); !ok {
		return fmt.Errorf("%s has no Route: %w", name, caddytls.ErrPermissionDenied)
	}
	return nil
}

var (
	_ caddy.Provisioner           = (*Permission)(nil)
	_ caddytls.OnDemandPermission = (*Permission)(nil)
	_ caddy.Provisioner           = (*Router)(nil)
	_ caddyhttp.MiddlewareHandler = (*Router)(nil)
	_ caddy.Provisioner           = (*Transport)(nil)
	_ caddy.CleanerUpper          = (*Transport)(nil)
	_ http.RoundTripper           = (*Transport)(nil)
)
