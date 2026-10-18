package harness

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/credential"
	"github.com/hyprsh/tnl/internal/edge/routes"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/outpost"
	"github.com/hyprsh/tnl/internal/render"
	"github.com/hyprsh/tnl/internal/state"
)

// This file holds the helpers that tests of more than one package use. The
// harness tests are split across packages (this one, bounds, caddysuite, zones
// and outposts) so that `go test` runs them side by side; the other packages
// dot-import this one.

func CtxFor(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return c
}

func Ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

type OutpostLine = outpost.DeclaredRoute

// WaitServed waits until the Visitor's request for host gets an answer: a
// declared Hostname's certificate is obtained in the background, so the first
// requests may find none yet.
func WaitServed(t *testing.T, re *RealEdge, host string) {
	t.Helper()
	WaitFor(t, host+" to be served", 30*time.Second, func() bool {
		req, _ := http.NewRequestWithContext(Ctx(t), "GET", "https://"+host+"/", nil)
		resp, err := re.Caddy.Client(t, H1).Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 200
	})
}

// DeclaredEdge is what an operator holds before the Edge first starts: the
// Edge's private key (from `tnl keygen`) and the port its credentials name.
type DeclaredEdge struct {
	Key  keys.Key
	Port int
}

func NewDeclaredEdge(t *testing.T) DeclaredEdge {
	t.Helper()
	priv, _, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return DeclaredEdge{Key: priv, Port: freePort(t, true)}
}

func GetAccepting(t *testing.T, ce *CaddyEdge, proto Proto, host, accept string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(Ctx(t), "GET", "https://"+host+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := ce.Client(t, proto).Do(req)
	if err != nil {
		t.Fatalf("%s: %v", proto, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s: reading body: %v", proto, err)
	}
	return resp, string(b)
}

// NewLogCapture is a logger that writes the Edge's own format, at debug, into
// a buffer the test reads.
func NewLogCapture() (*slog.Logger, *LockedBuf) {
	c := new(LockedBuf)
	return logx.New(logx.Options{Level: slog.LevelDebug, Out: c}), c
}

const Photos = "photos.test"

// ClosedAddr is a loopback host:port nothing listens on.
func ClosedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// LockedBuf is a log sink that the Edge, the Outposts and the test use at once.
type LockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

// EdgePeers lists the public keys (hex) of the Edge's WireGuard peers.
func EdgePeers(t *testing.T, re *RealEdge) []string {
	t.Helper()
	out, err := re.Tunnel.Dev.IpcGet()
	if err != nil {
		t.Fatal(err)
	}
	var ks []string
	for _, line := range strings.Split(out, "\n") {
		if k, ok := strings.CutPrefix(line, "public_key="); ok {
			ks = append(ks, k)
		}
	}
	return ks
}

func EventService(t *testing.T) *Service {
	return NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sse" {
			fmt.Fprint(w, "ok")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		rc := http.NewResponseController(w)
		for {
			if _, err := fmt.Fprint(w, "data: x\n\n"); err != nil || rc.Flush() != nil {
				return
			}
			select {
			case <-time.After(20 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
	}))
}

func LineWith(log, sub string) string {
	for _, l := range strings.Split(log, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

const ShortOfflineAfter = 600 * time.Millisecond

func WaitFor(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func EchoHost() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.Host) })
}

// OpenStream opens an event stream on host and returns a func that reads the
// next event, reporting whether one arrived before the stream ended.
func OpenStream(t *testing.T, re *RealEdge, host string) (next func() bool) {
	t.Helper()
	req, _ := http.NewRequestWithContext(Ctx(t), "GET", "https://"+host+"/sse", nil)
	resp, err := re.Caddy.Client(t, H1).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	br := bufio.NewReader(resp.Body)
	return func() bool {
		line, err := br.ReadString('\n')
		return err == nil && line != ""
	}
}

// SSEHandler streams events until the Visitor or the connection goes away.
func SSEHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	rc := http.NewResponseController(w)
	for {
		if _, err := fmt.Fprint(w, "data: x\n\n"); err != nil || rc.Flush() != nil {
			return
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
	}
}

func EdgeStatus(t *testing.T, re *RealEdge) (api.EdgeStatus, string) {
	t.Helper()
	s, err := api.Call[api.EdgeStatus](Ctx(t), re.API, http.MethodGet, api.PathStatus, nil)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := render.EdgeStatus(&b, s); err != nil {
		t.Fatal(err)
	}
	return s, b.String()
}

// EdgeWithService starts an Edge with Caddy, one Outpost and a Route from
// app.test to a Service running handler.
func EdgeWithService(t *testing.T, handler http.Handler) *Harness {
	t.Helper()
	svc := NewHTTPService(t, handler)
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home"})
	h.Edge.StartCaddy(t, CaddyOptions{})
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "app.test", Outpost: o.Name, Service: svc.Addr()}})
	return h
}

// Get makes a GET to https://host/path as a Visitor on proto.
func Get(t *testing.T, ce *CaddyEdge, proto Proto, host, path string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(Ctx(t), "GET", "https://"+host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ce.Client(t, proto).Do(req)
	if err != nil {
		t.Fatalf("%s: %v", proto, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s: reading body: %v", proto, err)
	}
	return resp, string(b)
}

// Outpost does what `tnl credential <name> --address 127.0.0.1` does: the line
// for the Edge and the credential for the Outpost.
func (d DeclaredEdge) Outpost(t *testing.T, name string) (state.DeclaredOutpost, string) {
	t.Helper()
	cred, pub, err := credential.Mint(d.Key, name, "127.0.0.1", d.Port)
	if err != nil {
		t.Fatal(err)
	}
	return state.DeclaredOutpost{Name: name, Key: pub}, cred.Encode()
}

func (d DeclaredEdge) Options(opts EdgeOptions) EdgeOptions {
	opts.Key, opts.WireGuardPort = &d.Key, d.Port
	return opts
}

func (l *LockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *LockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
