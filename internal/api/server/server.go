// Package server holds the API's handlers and the listeners that serve them.
// One set of handlers is mounted on each listener with that listener's actor:
// tnl.sock and the Tunnel listener.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/edge/routes"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/state"
)

// Config is what the handlers need.
type Config struct {
	Store   *state.Store
	Log     *slog.Logger
	Version string
	Build   string // "full" or "outpost"
	// Revision is the VCS revision.
	Revision string
	Role     api.Role

	// Address and WireGuardPort go into join tokens: what Outposts dial.
	Address       string
	WireGuardPort int

	// Conns closes Tunnel connections after a change that ends them. Nil
	// closes nothing.
	Conns ConnCloser

	// Resolver does the DNS checks; nil uses net.DefaultResolver.
	Resolver Resolver

	// SwapPeer, if set, replaces an Outpost's WireGuard peer (old key out, new
	// key in, same Tunnel address). The join calls it after its reply is out.
	SwapPeer func(old, new keys.Key, address netip.Addr)
	// PreparePeer, if set, adds the new key as a peer with no Tunnel address
	// yet, before the join replies, so its handshake can complete early.
	PreparePeer func(new keys.Key) error
	// DropPeer removes an Outpost's WireGuard peer; `outpost rm` calls it.
	DropPeer func(key keys.Key, address netip.Addr)
	// RevokePeer replaces an Outpost's peer at once: the new key takes the
	// Tunnel address and the old key's peer goes. `outpost rekey` calls it.
	RevokePeer func(old, new keys.Key, address netip.Addr)

	// StateDir is where backup finds Caddy's storage.
	StateDir string

	// Liveness says which Outposts are Offline, for the warnings. Nil knows none.
	Liveness Liveness

	// Certs obtains Route Hostnames' certificates; nil waits for none.
	Certs Certificates
	// CertWait bounds how long `route add` waits for a certificate; zero is
	// DefaultCertWait.
	CertWait time.Duration

	// Wildcards does the DNS and Caddy side of wildcard Zones; nil refuses them.
	Wildcards Wildcards

	// TestDialer test-dials a Route's Service after `route add` and `route
	// edit`. Nil (no web side) skips the test dial.
	TestDialer routes.TestDialer
}

// DefaultCertWait is the spec's "about 60 s" wait for a certificate.
const DefaultCertWait = 60 * time.Second

// Certificates is the certificate manager as the handlers see it. Obtain makes
// sure Caddy manages the Hostname and returns once a certificate is held for
// it, or with an error when ctx ends first; Caddy keeps trying either way.
type Certificates interface {
	Obtain(ctx context.Context, hostname string) error
}

// Wildcards is what wildcard Zones need beyond the state: the DNS provider
// check, a Caddy reload and the wait for the certificate.
type Wildcards interface {
	// Check finds the DNS zone that holds the Zone by SOA lookup and refuses
	// a token that cannot read it. It returns the DNS zone. No error it
	// returns carries the token.
	Check(ctx context.Context, provider, token, zone string) (dnsZone string, err error)
	// Reload loads Caddy's config for the Zones as they now stand. It cuts
	// WebSockets and no other stream.
	Reload() error
	// Wait waits for a certificate for the name, or until ctx ends.
	Wait(ctx context.Context, name string) error
}

// Liveness is the Edge's view of the Outposts, as the handlers read it.
type Liveness interface {
	State(outpost string) liveness.State
}

// ConnCloser is the connection tracker as the handlers see it, in the
// domain's terms. The handlers call it after the store's lock is released.
type ConnCloser interface {
	CloseRoute(hostname string)
	CloseOutpost(name string)
}

// Server is the handler set. It has no listener of its own.
type Server struct {
	cfg Config
	mux *http.ServeMux
}

// New builds the handlers.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.routes()
	return s
}

// routes is the one place endpoints are listed. A new endpoint is one line.
func (s *Server) routes() {
	endpoint(s, http.MethodGet, api.PathInfo, s.info)
	endpoint(s, http.MethodGet, api.PathZones, s.zoneList)
	endpoint(s, http.MethodPost, api.PathZones, s.zoneAdd)
	endpoint(s, http.MethodPost, api.PathZoneRm, s.zoneRm)
	endpoint(s, http.MethodPost, api.PathOutposts, s.outpostAdd)
	endpoint(s, http.MethodPost, api.PathOutpostRm, s.outpostRm)
	endpoint(s, http.MethodPost, api.PathOutpostRekey, s.outpostRekey)
	endpoint(s, http.MethodGet, api.PathRoutes, s.routeList)
	endpoint(s, http.MethodPost, api.PathRoutes, s.routeAdd)
	endpoint(s, http.MethodPost, api.PathRouteRm, s.routeRm)
	endpoint(s, http.MethodPost, api.PathJoin, s.join)
	endpoint(s, http.MethodPost, api.PathRouteEdit, s.routeEdit)
	s.mux.HandleFunc("GET "+api.PathBackup, s.backup) // streams a tar, so not endpoint
}

// afterReplyGrace is how long after a reply is written the functions queued
// with afterReply run.
const afterReplyGrace = 150 * time.Millisecond

type actorKey struct{}

// Handler serves the API for callers whose actor is the given one.
func (s *Server) Handler(actor api.Actor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(api.VersionHeader, s.cfg.Version)
		after := &afterList{}
		ctx := context.WithValue(context.WithValue(r.Context(), actorKey{}, actor), afterKey{}, after)
		s.mux.ServeHTTP(w, r.WithContext(ctx))
		if len(after.fns) > 0 {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// The reply is out; give it a moment to cross the Tunnel.
			fns := after.fns
			time.AfterFunc(afterReplyGrace, func() {
				for _, fn := range fns {
					fn()
				}
			})
		}
	})
}

// HandlerBySource serves the API for callers whose actor comes from the
// connection's source address, as the Tunnel listener's does. who maps the
// source (host:port) to an actor, or says why there is none; a caller with no
// actor is refused with 403 and its request goes no further.
func (s *Server) HandlerBySource(who func(remote string) (api.Actor, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, err := who(r.RemoteAddr)
		if err != nil {
			w.Header().Set(api.VersionHeader, s.cfg.Version)
			s.cfg.Log.Warn("request refused", "source", r.RemoteAddr, "method", r.Method, "path", r.URL.Path, "error", err.Error())
			s.reply(w, nil, api.Errorf(http.StatusForbidden, "%v", err))
			return
		}
		s.Handler(actor).ServeHTTP(w, r)
	})
}

// Serve answers on ln, with every caller the given actor, until ctx ends.
func (s *Server) Serve(ctx context.Context, ln net.Listener, actor api.Actor) error {
	return s.ServeHandler(ctx, ln, s.Handler(actor))
}

// ServeHandler answers on ln with h until ctx ends.
func (s *Server) ServeHandler(ctx context.Context, ln net.Listener, h http.Handler) error {
	hs := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := hs.Shutdown(sctx)
	if err != nil {
		hs.Close()
	}
	<-errc
	return err
}

// ListenSocket listens on tnl.sock in dir with mode 0600; see api.ListenSocket.
func ListenSocket(dir string) (net.Listener, error) { return api.ListenSocket(dir) }

// endpoint registers a handler that takes a decoded request and returns a
// response. The request body is decoded strictly: an unknown field refuses it
// whole, before the handler runs. GET handlers take struct{} and read no body.
// A handler returns *api.Error to refuse; the actor is what the listener set.
func endpoint[Req, Resp any](s *Server, method, path string, fn func(ctx context.Context, actor api.Actor, req Req) (Resp, error)) {
	s.mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
		actor, _ := r.Context().Value(actorKey{}).(api.Actor)
		var req Req
		if method != http.MethodGet {
			if err := s.decode(r, &req); err != nil {
				s.reply(w, nil, err)
				return
			}
		}
		resp, err := fn(r.Context(), actor, req)
		var ae *api.Error
		if errors.As(err, &ae) && ae.Status == http.StatusForbidden {
			s.cfg.Log.Warn("request refused", "actor", actor.String(), "method", r.Method, "path", r.URL.Path, "error", ae.Message)
		}
		s.reply(w, resp, err)
	})
}

func (s *Server) decode(r *http.Request, v any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		return api.Errorf(http.StatusBadRequest, "reading the request: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			return api.Errorf(http.StatusBadRequest,
				"the Edge (%s) doesn't support the field %s; it needs a newer version; upgrade the Edge", s.cfg.Version, field)
		}
		return api.Errorf(http.StatusBadRequest, "the request is not valid: %v", err)
	}
	if dec.More() {
		return api.Errorf(http.StatusBadRequest, "the request is not valid: more than one JSON value")
	}
	return nil
}

func (s *Server) reply(w http.ResponseWriter, resp any, err error) {
	w.Header().Set("Content-Type", "application/json")
	// A response with a Content-Length is complete once it is flushed.
	send := func(status int, v any) {
		b, merr := json.Marshal(v)
		if merr != nil {
			status, b = http.StatusInternalServerError, []byte(`{"error":"encoding the response failed"}`)
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b)+1))
		w.WriteHeader(status)
		w.Write(append(b, '\n'))
	}
	if err != nil {
		var ae *api.Error
		var inv *state.InvalidError
		switch {
		case errors.As(err, &ae):
		case errors.As(err, &inv):
			ae = api.Errorf(http.StatusBadRequest, "%v", err)
		default:
			s.cfg.Log.Error("api request failed", "error", err)
			ae = api.Errorf(http.StatusInternalServerError, "%v", err)
		}
		send(ae.Status, struct {
			Error string `json:"error"`
		}{ae.Message})
		return
	}
	send(http.StatusOK, resp)
}

func (s *Server) info(_ context.Context, _ api.Actor, _ struct{}) (api.Info, error) {
	return api.Info{Role: s.cfg.Role, Version: s.cfg.Version, Build: s.cfg.Build, Revision: s.cfg.Revision}, nil
}
