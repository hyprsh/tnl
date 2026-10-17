package outpost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/logx"
)

// notConnected is what the CLI prints when the relay can't reach the Edge.
const notConnected = "Outpost not connected: the Edge did not answer through the Tunnel"

// defaultRelayDialTimeout bounds one attempt to reach the Edge's API.
const defaultRelayDialTimeout = 5 * time.Second

// relay is the Outpost's tnl.sock: it forwards the local CLI's API calls to the
// Edge unchanged, replies included, and answers info and status itself.
type relay struct {
	info api.Info
	log  *slog.Logger
	dial func(ctx context.Context, address string) (net.Conn, error)
	// timeout bounds reaching the Edge.
	timeout time.Duration
	// status builds the answer to GET PathStatus; nil leaves it to the Edge.
	status func(ctx context.Context) api.OutpostStatus
}

func (r *relay) handler() http.Handler {
	edge := &url.URL{Scheme: "http", Host: net.JoinHostPort(addr.Edge.String(), strconv.Itoa(api.TunnelPort))}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(edge)
			pr.Out.Header.Del("X-Forwarded-For")
		},
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
				ctx, cancel := context.WithTimeout(ctx, r.timeout)
				defer cancel()
				return r.dial(ctx, address)
			},
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			r.log.Warn("the relay could not reach the Edge", "method", req.Method, "path", req.URL.Path, "error", err.Error())
			r.reply(w, http.StatusServiceUnavailable, api.ErrorBody{Error: notConnected})
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && req.URL.Path == api.PathInfo {
			r.reply(w, http.StatusOK, r.info)
			return
		}
		if req.Method == http.MethodGet && req.URL.Path == api.PathLogs {
			// This Outpost's own ring, never the Edge's: not forwarded.
			w.Header().Set(api.VersionHeader, r.info.Version)
			if err := api.ServeLogs(w, req, logx.RingOf(r.log)); err != nil {
				status, msg := http.StatusBadRequest, err.Error()
				var ae *api.Error
				if errors.As(err, &ae) {
					status, msg = ae.Status, ae.Message
				}
				r.reply(w, status, api.ErrorBody{Error: msg})
			}
			return
		}
		if req.Method == http.MethodGet && req.URL.Path == api.PathStatus && r.status != nil {
			r.reply(w, http.StatusOK, r.status(req.Context()))
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		var hostname string
		if routeChange(req) {
			hostname = peekHostname(req)
		}
		proxy.ServeHTTP(rec, req)
		if routeChange(req) {
			r.log.Info("route change through this Outpost's CLI", "method", req.Method, "path", req.URL.Path,
				"hostname", hostname, "status", rec.status)
		}
	})
}

func (r *relay) reply(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	w.Header().Set(api.VersionHeader, r.info.Version)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)+1))
	w.WriteHeader(status)
	w.Write(append(b, '\n'))
}

// routeChange reports whether the request adds, edits or removes a Route.
func routeChange(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	switch req.URL.Path {
	case api.PathRoutes, api.PathRouteEdit, api.PathRouteRm:
		return true
	}
	return false
}

// peekHostname reads the request's body for its hostname and puts it back.
func peekHostname(req *http.Request) string {
	raw, err := io.ReadAll(http.MaxBytesReader(nil, req.Body, 1<<20))
	req.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	var b struct {
		Hostname string `json:"hostname"`
	}
	json.Unmarshal(raw, &b)
	return b.Hostname
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
