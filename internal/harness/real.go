package harness

import (
	"cmp"
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
	"github.com/hyprsh/tnl/internal/edge"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/edge/routes"
	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/outpost"
	"github.com/hyprsh/tnl/internal/state"
)

// RealEdge is the Edge as `tnl edge` runs it, built by edge.Run on a real state
// directory, with its API on tnl.sock. It embeds an *Edge, so Dial, Raw and
// StartCaddy work on it as they do on the in-process one. Outposts join it
// the way `tnl connect --token` does, with no root, TUN device or kernel module.
type RealEdge struct {
	*Edge
	StateDir string      // short enough for tnl.sock
	API      *api.Client // talks to tnl.sock
	Store    *state.Store
	// Liveness is the Edge's own: what it knows of each Outpost's reachability.
	Liveness *liveness.Liveness

	stop         func()
	pingInterval time.Duration
}

// harnessVersion is the version the real Edge, the Outposts and the API client
// all report, so the CLI-style exact-version check passes.
const harnessVersion = "v0.0.0-test"

// EdgeOptions tunes StartEdgeWith. The zero value is StartEdge: no Caddy.
type EdgeOptions struct {
	// StateDir reuses a state directory, as a restarted Edge does. Empty makes
	// a fresh temporary one.
	StateDir string
	// Pebble starts Caddy beside the Tunnel, as `tnl edge` does, getting its
	// certificates from this ACME CA. RealEdge.Caddy is then set. Caddy is a
	// process singleton, so only one such Edge runs at a time.
	Pebble *Pebble
	// LogOut, if set, also receives the Edge's log lines, at warn and above.
	LogOut io.Writer
	// Resolver answers the Edge's DNS checks. Nil answers every name with
	// 127.0.0.1, the Edge's --address, so no check warns and none uses real DNS.
	Resolver server.Resolver
	// ServiceRoots are the roots the Edge trusts for https:// Services, in place
	// of the system's. Needs Pebble (Caddy).
	ServiceRoots *x509.CertPool
	// WireGuardPort fixes the UDP port, as a restored Edge keeps the one its
	// Outposts' credentials name. Zero picks a free one.
	WireGuardPort int
	// LogInfo lets LogOut receive info lines too.
	LogInfo bool
	// OfflineAfter, LivenessPoll and PingInterval shrink the spec's 60 s, the
	// Edge's check interval and the 10 s ping, for this Edge and for the
	// Outposts that Connect to it. Zero keeps the spec's values, except that
	// the Edge checks every 20 ms and Outposts ping every 100 ms by default.
	OfflineAfter, LivenessPoll, PingInterval time.Duration
}

// atEdge answers every name with the Edge's address.
type atEdge struct{}

func (atEdge) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}

// StartEdge runs edge.Run on a temporary state directory, WireGuard on a free
// loopback-reachable port and --address 127.0.0.1, without Caddy. It stops when
// the test ends.
func StartEdge(t testing.TB) *RealEdge { return StartEdgeWith(t, EdgeOptions{}) }

// StartEdgeWith is StartEdge with options.
func StartEdgeWith(t testing.TB, opts EdgeOptions) *RealEdge {
	t.Helper()
	dir := opts.StateDir
	if dir == "" {
		// Unix socket paths are short on macOS, so keep the directory name short.
		var err error
		dir, err = os.MkdirTemp("", "tnl")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
	}

	cfg := edge.Config{
		Address: "127.0.0.1", StateDir: dir, Version: harnessVersion, Build: "full", Revision: "test",
		Log:      logx.New(logx.Options{Level: slog.LevelWarn, Out: testWriter{t}}),
		Resolver: opts.Resolver, WireGuardPort: opts.WireGuardPort,
	}
	if cfg.Resolver == nil {
		cfg.Resolver = atEdge{}
	}
	cfg.LivenessPoll = cmp.Or(opts.LivenessPoll, 20*time.Millisecond)
	cfg.OfflineAfter = opts.OfflineAfter // zero is the spec's 60 s
	if opts.LogOut != nil {
		level := slog.LevelWarn
		if opts.LogInfo {
			level = slog.LevelInfo
		}
		cfg.Log = logx.New(logx.Options{Level: level, Out: io.MultiWriter(testWriter{t}, opts.LogOut)})
	}
	var ce *CaddyEdge
	if p := opts.Pebble; p != nil {
		httpsPort := freePort(t, true)
		httpPort := freePort(t, false)
		cfg.Web = &edge.Web{
			HTTPListen: fmt.Sprintf("127.0.0.1:%d", httpPort), HTTPSListen: fmt.Sprintf("127.0.0.1:%d", httpsPort),
			HTTPPort: httpPort, HTTPSPort: httpsPort,
			ACMECA: p.DirectoryURL, ACMEEmail: "ops@example.com", ACMETrustedRoots: []string{p.certFile}, ServiceRoots: opts.ServiceRoots,
		}
		ce = &CaddyEdge{HTTPPort: httpPort, HTTPSPort: httpsPort, StateDir: dir, Roots: x509.NewCertPool()}
		ce.Roots.AddCert(p.Root)
		// Caddy's data directory comes from the environment; put it back afterwards.
		if old, ok := os.LookupEnv("XDG_DATA_HOME"); ok {
			t.Cleanup(func() { os.Setenv("XDG_DATA_HOME", old) })
		} else {
			t.Cleanup(func() { os.Unsetenv("XDG_DATA_HOME") })
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	up := make(chan *edge.Running, 1)
	done := make(chan error, 1)
	cfg.Started = func(r *edge.Running) { up <- r }
	go func() { done <- edge.Run(ctx, cfg) }()
	var stopped bool
	stop := func() {
		if !stopped {
			stopped = true
			cancel()
			<-done
		}
	}
	t.Cleanup(stop)
	var r *edge.Running
	select {
	case r = <-up:
	case err := <-done:
		stopped = true
		t.Fatalf("edge.Run: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the Edge did not start")
	}
	e := &Edge{Routes: new(routes.Mem), Outposts: new(routes.MemOutposts), Tunnel: r.Tunnel, Net: r.Tunnel.Net, Port: r.WireGuardPort, Caddy: ce}
	return &RealEdge{
		Edge:     e,
		StateDir: dir,
		API:      &api.Client{StateDir: dir, Version: harnessVersion},
		Store:    r.Store,
		Liveness: r.Liveness,
		stop:     stop,

		pingInterval: cmp.Or(opts.PingInterval, 100*time.Millisecond),
	}
}

// Stop stops the Edge, as a restart would. The test's end stops it too.
func (e *RealEdge) Stop() { e.stop() }

// AddZone calls `zone add` on the running Edge.
func (e *RealEdge) AddZone(t testing.TB, name string) {
	t.Helper()
	if _, err := api.Call[api.ZoneAddReply](context.Background(), e.API, http.MethodPost, api.PathZones, api.ZoneAddRequest{Name: name}); err != nil {
		t.Fatal(err)
	}
}

// AddRoute calls `route add` on the running Edge.
func (e *RealEdge) AddRoute(t testing.TB, hostname, serviceURL, outpost string) {
	t.Helper()
	_, err := api.Call[api.RouteAddReply](context.Background(), e.API, http.MethodPost, api.PathRoutes,
		api.RouteAddRequest{Hostname: hostname, Service: serviceURL, Outpost: outpost})
	if err != nil {
		t.Fatal(err)
	}
}

// AddOutpost calls `outpost add` on the running Edge and returns the join token.
func (e *RealEdge) AddOutpost(t testing.TB, name string) string {
	t.Helper()
	return e.AddOutpostTTL(t, name, "")
}

// AddOutpostTTL is AddOutpost with `--ttl`; empty is the default.
func (e *RealEdge) AddOutpostTTL(t testing.TB, name, ttl string) string {
	t.Helper()
	resp, err := api.Call[api.OutpostAddResponse](context.Background(), e.API, http.MethodPost, api.PathOutposts, api.OutpostAddRequest{Name: name, TTL: ttl})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Token
}

// JoinedOutpost is an Outpost started with outpost.Run.
type JoinedOutpost struct {
	*Outpost
	API      *api.Client // calls the Edge's API through the Tunnel
	Token    string
	StateDir string
	Stop     func() error // stops the Outpost; the test's end does too
}

// JoinOutpost adds an Outpost on the Edge, runs `tnl connect --token` for it in
// process, waits for the Tunnel to come up and registers it for Dial.
func (e *RealEdge) JoinOutpost(t testing.TB, name string) *JoinedOutpost {
	t.Helper()
	tok := e.AddOutpost(t, name)
	return e.Connect(t, name, tok, mustTempDir(t))
}

// Connect runs an Outpost with the given token and state directory, as
// `tnl connect` would. Pass an empty token to reconnect from the state.
func (e *RealEdge) Connect(t testing.TB, name, tok, stateDir string) *JoinedOutpost {
	t.Helper()
	j, err := e.TryConnect(t, name, tok, stateDir)
	if err != nil {
		t.Fatalf("outpost.Run: %v", err)
	}
	return j
}

// ConnectOptions tunes an Outpost started by TryConnectWith.
type ConnectOptions struct {
	// LogOut, if set, receives the Outpost's log lines at info and above.
	LogOut io.Writer
	// JoinTimeout bounds the join exchange; zero uses the Outpost's default.
	JoinTimeout time.Duration
}

// TryConnect is Connect that returns the error `tnl connect` would exit on.
func (e *RealEdge) TryConnect(t testing.TB, name, tok, stateDir string) (*JoinedOutpost, error) {
	t.Helper()
	return e.TryConnectWith(t, name, tok, stateDir, ConnectOptions{JoinTimeout: 3 * time.Second})
}

// TryConnectWith is TryConnect with options.
func (e *RealEdge) TryConnectWith(t testing.TB, name, tok, stateDir string, opts ConnectOptions) (*JoinedOutpost, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	up := make(chan *outpost.Running, 1)
	done := make(chan error, 1)
	var out io.Writer = testWriter{t}
	if opts.LogOut != nil {
		out = io.MultiWriter(out, opts.LogOut)
	}
	go func() {
		done <- outpost.Run(ctx, outpost.Config{
			Token: tok, StateDir: stateDir, Version: harnessVersion, Build: "outpost", Revision: "test",
			Log:          logx.New(logx.Options{Level: slog.LevelInfo, Out: out}),
			JoinTimeout:  opts.JoinTimeout,
			Started:      func(r *outpost.Running) { up <- r },
			PingInterval: e.pingInterval,
		})
	}()
	stop := func() error { cancel(); return <-done }
	var stopped bool
	t.Cleanup(func() {
		if !stopped {
			stop()
		}
	})
	var r *outpost.Running
	select {
	case r = <-up:
	case err := <-done:
		stopped = true
		return nil, err
	case <-time.After(10 * time.Second):
		t.Fatal("the Outpost did not start")
	}

	// The Edge learns the Outpost's endpoint from its first packet.
	hctx, hcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer hcancel()
	if err := e.waitHandshake(hctx, r.Address); err != nil {
		t.Fatal(err)
	}
	o := &Outpost{Name: name, Addr: r.Address, Tunnel: r.Tunnel, Net: r.Tunnel.Net}
	e.Outposts.Set(name, o.DataAddr())
	return &JoinedOutpost{Outpost: o, API: r.API, Token: tok, StateDir: stateDir, Stop: func() error { stopped = true; return stop() }}, nil
}

// waitHandshake waits until the Edge has completed a handshake with the peer
// whose Tunnel address is a.
func (e *RealEdge) waitHandshake(ctx context.Context, a netip.Addr) error {
	for {
		out, err := e.Tunnel.Dev.IpcGet()
		if err != nil {
			return err
		}
		// One block per peer, each starting at its public_key line.
		for _, block := range strings.Split(out, "public_key=")[1:] {
			if strings.Contains(block, "\nallowed_ip="+a.String()+"/32\n") &&
				strings.Contains(block, "\nlast_handshake_time_sec=") &&
				!strings.Contains(block, "\nlast_handshake_time_sec=0\n") {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for the Edge's handshake with %s: %w", a, ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func mustTempDir(t testing.TB) string {
	t.Helper()
	return t.TempDir() + "/outpost"
}

// testWriter sends log lines to the test log.
type testWriter struct{ t testing.TB }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
