// Package server holds the API's handlers and the listeners that serve them.
// One set of handlers is mounted on each listener with that listener's actor:
// tnl.sock and the Tunnel listener.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/state"
)

// Config is what the handlers need.
type Config struct {
	Store   *state.Store
	Log     *slog.Logger
	Version string
	Build   string
	Role    api.Role

	// Address and WireGuardPort go into join tokens: what Outposts dial.
	Address       string
	WireGuardPort int

	// Conns closes Tunnel connections after a change that ends them. Nil
	// closes nothing.
	Conns ConnCloser
}

// ConnCloser is the connection tracker as the handlers see it, in the
// domain's terms. The handlers call it after the store's lock is released.
type ConnCloser interface {
	CloseRoute(hostname string)
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
	endpoint(s, http.MethodPost, api.PathOutposts, s.outpostAdd)
	endpoint(s, http.MethodGet, api.PathRoutes, s.routeList)
	endpoint(s, http.MethodPost, api.PathRoutes, s.routeAdd)
	endpoint(s, http.MethodPost, api.PathRouteRm, s.routeRm)
}

type actorKey struct{}

// Handler serves the API for callers whose actor is the given one.
func (s *Server) Handler(actor api.Actor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(api.VersionHeader, s.cfg.Version)
		s.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, actor)))
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

// ListenSocket listens on tnl.sock in dir with mode 0600. It replaces a socket
// file nothing answers on, and refuses one that is live.
func ListenSocket(dir string) (net.Listener, error) {
	path := filepath.Join(dir, api.SocketName)
	if _, err := os.Lstat(path); err == nil {
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("another process is already serving %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		if len(path) > 100 {
			return nil, fmt.Errorf("%w (the socket path %s is too long for a Unix socket; use a shorter state directory)", err, path)
		}
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

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
		w.WriteHeader(ae.Status)
		json.NewEncoder(w).Encode(struct {
			Error string `json:"error"`
		}{ae.Message})
		return
	}
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) info(_ context.Context, _ api.Actor, _ struct{}) (api.Info, error) {
	return api.Info{Role: s.cfg.Role, Version: s.cfg.Version, Build: s.cfg.Build}, nil
}
