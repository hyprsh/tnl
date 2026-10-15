package caddyx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddytls"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

func init() {
	caddy.RegisterModule(Router{})
	caddy.RegisterModule(Transport{})
	caddy.RegisterModule(Permission{})
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

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request, next caddyhttp.Handler) error {
	route, ok := r.rt.Table.Lookup(hostOnly(req.Host))
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return nil
	}
	ctx := req.Context()
	caddyhttp.SetVar(ctx, varRoute, route)
	caddyhttp.SetVar(ctx, varRequestID, newRequestID())
	return next.ServeHTTP(w, req)
}

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return strings.Trim(h, "[]")
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
// bare 502; the error page, 503 and dial retries come in a later slice.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	route, ok := caddyhttp.GetVar(ctx, varRoute).(routes.Route)
	if !ok {
		return nil, errors.New("tnl: request reached the transport without a Route")
	}
	addr, ok := t.rt.Outposts.Addr(route.Outpost)
	if !ok {
		return bad(req), nil
	}
	id, _ := caddyhttp.GetVar(ctx, varRequestID).(string)
	out := req.Clone(context.WithValue(ctx, requestIDKey, id))
	out.URL.Scheme = "http"
	out.URL.Host = route.Hostname

	resp, err := t.rt.pool.transport(t.rt, route, addr).RoundTrip(out)
	if err != nil {
		var re *ReplyError
		if errors.As(err, &re) {
			return bad(req), nil
		}
		return nil, err
	}
	return resp, nil
}

func bad(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusBadGateway,
		Status:     "502 Bad Gateway",
		Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:  http.Header{},
		Body:    http.NoBody,
		Request: req,
	}
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
