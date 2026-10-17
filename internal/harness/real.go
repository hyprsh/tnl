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

	"github.com/hyprsh/tnl/internal/allow"
	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
	"github.com/hyprsh/tnl/internal/declare"
	"github.com/hyprsh/tnl/internal/edge"
	"github.com/hyprsh/tnl/internal/edge/caddyx"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/edge/routes"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/outpost"
	"github.com/hyprsh/tnl/internal/remind"
	"github.com/hyprsh/tnl/internal/state"
	"github.com/hyprsh/tnl/internal/udpbuf"
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
	// Certs is the certificate manager; nil unless the Edge has Caddy.
	Certs *caddyx.Certs

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
	// LogOut, if set, also receives the Edge's log lines at LogLevel and above.
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
	// LogLevel is the lowest level LogOut receives; the zero value is info.
	LogLevel slog.Level
	// AccessLog is --access-log. DrainTimeout shrinks the 10 s of stopping.
	AccessLog    bool
	DrainTimeout time.Duration
	// OfflineAfter, SilentAfter, LivenessPoll and PingInterval shrink the spec's
	// 60 s, the 20 s of silence after which the Edge closes an Outpost's Tunnel
	// connections, the Edge's check interval and the 10 s ping, for this Edge
	// and for the Outposts that Connect to it. Zero keeps the spec's values,
	// except that the Edge checks every 20 ms and Outposts ping every 100 ms by
	// default.
	OfflineAfter, SilentAfter, LivenessPoll, PingInterval time.Duration
	// CertWait shortens the certificate wait at `route add`; zero keeps 60 s.
	CertWait time.Duration
	// Address is the Edge's --address, a name or an IP the Outposts resolve; empty
	// is 127.0.0.1.
	Address string
	// Reminders shortens the schedule of the Edge's reminders for waiting lines;
	// zero keeps the spec's.
	Reminders remind.Schedule
	// Version is the version this Edge reports and judges by; empty is the
	// harness's own. Each process takes its version here, so a test can run
	// an Edge and an Outpost of different versions in one binary.
	Version string
	// Key is the Edge's private key (TNL_KEY), KeySource what it is called in
	// the refusal; Zones and Outposts are the declared lines, as `tnl edge` parses
	// them from TNL_ZONE_* and TNL_OUTPOST_*.
	Key       *keys.Key
	KeySource string
	Zones     []state.DeclaredZone
	Outposts  []state.DeclaredOutpost
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
	re, err := TryStartEdgeWith(t, opts)
	if err != nil {
		t.Fatalf("edge.Run: %v", err)
	}
	return re
}

// TryStartEdgeWith is StartEdgeWith that returns the error edge.Run refuses to
// start with, which `tnl edge` would exit on.
func TryStartEdgeWith(t testing.TB, opts EdgeOptions) (*RealEdge, error) {
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
		Address: cmp.Or(opts.Address, "127.0.0.1"), StateDir: dir, Version: cmp.Or(opts.Version, harnessVersion), Build: "full", Revision: "test",
		Log:      logx.New(logx.Options{Level: slog.LevelWarn, Out: testWriter{t}}),
		Resolver: opts.Resolver, WireGuardPort: opts.WireGuardPort, CertWait: opts.CertWait,
		DNSResolvers: []string{fakeDNSServer(t)}, UDPBuffers: grantedInFull,
	}
	if cfg.Resolver == nil {
		cfg.Resolver = atEdge{}
	}
	cfg.LivenessPoll = cmp.Or(opts.LivenessPoll, 20*time.Millisecond)
	cfg.OfflineAfter = opts.OfflineAfter // zero is the spec's 60 s
	cfg.SilentAfter = opts.SilentAfter
	cfg.Reminders = opts.Reminders
	cfg.Key, cfg.KeySource, cfg.Zones, cfg.Outposts = opts.Key, cmp.Or(opts.KeySource, "TNL_KEY"), opts.Zones, opts.Outposts
	if opts.LogOut != nil {
		cfg.Log = logx.New(logx.Options{Level: opts.LogLevel, Out: io.MultiWriter(testWriter{t}, opts.LogOut)})
	}
	var ce *CaddyEdge
	if p := opts.Pebble; p != nil {
		httpsPort := freePort(t, true)
		httpPort := freePort(t, false)
		cfg.Web = &edge.Web{
			HTTPListen: fmt.Sprintf("127.0.0.1:%d", httpPort), HTTPSListen: fmt.Sprintf("127.0.0.1:%d", httpsPort),
			HTTPPort: httpPort, HTTPSPort: httpsPort,
			ACMECA: p.DirectoryURL, ACMEEmail: "ops@example.com", ACMETrustedRoots: []string{p.certFile}, ServiceRoots: opts.ServiceRoots,
			AccessLog: opts.AccessLog, DrainTimeout: opts.DrainTimeout,
			// The fake provider's records are not in DNS to be found.
			DNSPropagationTimeout: -1,
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
		return nil, err
	case <-time.After(30 * time.Second):
		t.Fatal("the Edge did not start")
	}
	e := &Edge{Routes: new(routes.Mem), Outposts: new(routes.MemOutposts), Tunnel: r.Tunnel, Net: r.Tunnel.Net, Port: r.WireGuardPort, Caddy: ce}
	return &RealEdge{
		Edge:     e,
		StateDir: dir,
		API:      &api.Client{StateDir: dir, Version: cmp.Or(opts.Version, harnessVersion)},
		Store:    r.Store,
		Liveness: r.Liveness,
		Certs:    r.Certs,
		stop:     stop,

		pingInterval: cmp.Or(opts.PingInterval, 100*time.Millisecond),
	}, nil
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
	e.AddRouteReply(t, hostname, serviceURL, outpost)
}

// AddRouteReply is AddRoute that returns the reply, warnings included. Each
// option adjusts the request, as a flag does.
func (e *RealEdge) AddRouteReply(t testing.TB, hostname, serviceURL, outpost string, opts ...func(*api.RouteAddRequest)) api.RouteAddReply {
	t.Helper()
	req := api.RouteAddRequest{Hostname: hostname, Service: serviceURL, Outpost: outpost}
	for _, f := range opts {
		f(&req)
	}
	r, err := api.Call[api.RouteAddReply](context.Background(), e.API, http.MethodPost, api.PathRoutes, req)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// AddOutpost calls `outpost add` on the running Edge and returns the join token.
func (e *RealEdge) AddOutpost(t testing.TB, name string) string {
	t.Helper()
	return e.AddOutpostTTL(t, name, "")
}

// AddOutpostTTL is AddOutpost with `--ttl`; empty is the default.
func (e *RealEdge) AddOutpostTTL(t testing.TB, name, ttl string) string {
	t.Helper()
	resp, err := api.Call[api.OutpostAddReply](context.Background(), e.API, http.MethodPost, api.PathOutposts, api.OutpostAddRequest{Name: name, TTL: ttl})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Token
}

// JoinedOutpost is an Outpost started with outpost.Run.
type JoinedOutpost struct {
	*Outpost
	API      *api.Client // calls the Edge's API through the Tunnel
	CLI      *api.Client // the local CLI: talks to the Outpost's relay socket in StateDir
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
	// Resolve is the Outpost's resolver for the Edge's address; nil is the system's.
	Resolve func(ctx context.Context, host string) (netip.Addr, error)
	// NoAnswerAfter and Reminders shorten the 30 s of silence and the reminder
	// schedule; zero keeps the spec's.
	NoAnswerAfter time.Duration
	Reminders     remind.Schedule
	// Allow is the Outpost's --allow list; nil allows every Service.
	Allow *allow.List
	// Version is the version this Outpost reports and judges by; empty is the
	// harness's own.
	Version string
	// Declaration is the Routes the Outpost declares, as the TNL_ROUTE_* lines
	// would give them.
	Declaration []outpost.DeclaredRoute
	// Credential is the Outpost credential (tnlc1.…) a declared Outpost connects
	// with, in place of a join token.
	Credential string
	// NoHandshake returns as soon as the Outpost runs, without waiting for the
	// Edge to hear from it: for an Outpost that is meant to be turned away.
	NoHandshake bool
}

// DeclaredLine makes a line of an Outpost's Declaration, labelled as the
// variable TNL_ROUTE_<label> would be.
func DeclaredLine(label, hostname, service string) outpost.DeclaredRoute {
	return outpost.DeclaredRoute{
		Line:  declare.Line{Hostname: hostname, Service: service},
		Label: "TNL_ROUTE_" + label,
	}
}

// ConnectCredential runs a declared Outpost with its Outpost credential, as
// `tnl connect` would with only TNL_CREDENTIAL set.
func (e *RealEdge) ConnectCredential(t testing.TB, name, cred string, opts ConnectOptions) *JoinedOutpost {
	t.Helper()
	opts.Credential = cred
	j, err := e.TryConnectWith(t, name, "", mustTempDir(t), opts)
	if err != nil {
		t.Fatalf("outpost.Run: %v", err)
	}
	return j
}

// ConnectWith is Connect with options, for an Outpost that joins by token.
func (e *RealEdge) ConnectWith(t testing.TB, name, tok string, opts ConnectOptions) *JoinedOutpost {
	t.Helper()
	if opts.JoinTimeout == 0 {
		opts.JoinTimeout = 3 * time.Second
	}
	j, err := e.TryConnectWith(t, name, tok, mustTempDir(t), opts)
	if err != nil {
		t.Fatalf("outpost.Run: %v", err)
	}
	return j
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
			Token: tok, StateDir: stateDir, Version: cmp.Or(opts.Version, harnessVersion), Build: "outpost", Revision: "test",
			Log:         logx.New(logx.Options{Level: slog.LevelInfo, Out: out}),
			JoinTimeout: opts.JoinTimeout, RelayDialTimeout: 2 * time.Second, Allow: opts.Allow,
			Started:      func(r *outpost.Running) { up <- r },
			PingInterval: e.pingInterval,
			Resolve:      opts.Resolve, NoAnswerAfter: opts.NoAnswerAfter, Reminders: opts.Reminders,
			Declaration: opts.Declaration, Credential: opts.Credential, UDPBuffers: grantedInFull,
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

	if opts.NoHandshake {
		return &JoinedOutpost{Outpost: &Outpost{Name: name, Addr: r.Address, Tunnel: r.Tunnel, Net: r.Tunnel.Net}, API: r.API, StateDir: stateDir, Stop: func() error { stopped = true; return stop() }}, nil
	}
	// The Edge learns the Outpost's endpoint from its first packet.
	hctx, hcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer hcancel()
	if err := e.waitHandshake(hctx, r.Address); err != nil {
		t.Fatal(err)
	}
	// The Edge's liveness hears of the Outpost a poll later; a test dial of a
	// Route added right after joining must find it online.
	for deadline := time.Now().Add(3 * time.Second); e.Liveness != nil && e.Liveness.State(name) != liveness.Online && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	o := &Outpost{Name: name, Addr: r.Address, Tunnel: r.Tunnel, Net: r.Tunnel.Net}
	e.Outposts.Set(name, o.DataAddr())
	return &JoinedOutpost{Outpost: o, API: r.API, CLI: &api.Client{StateDir: stateDir, Version: cmp.Or(opts.Version, harnessVersion)}, Token: tok, StateDir: stateDir, Stop: func() error { stopped = true; return stop() }}, nil
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
	// The Outpost's tnl.sock lives here, so keep the path short.
	dir, err := os.MkdirTemp("", "tnlo")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir + "/outpost"
}

// testWriter sends log lines to the test log.
type testWriter struct{ t testing.TB }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// grantedInFull answers the UDP buffer measurement as fully granted, so the
// host's own limits (low on a CI runner) put no warn line into tests that count
// warnings. internal/edge and internal/outpost test the warn itself.
func grantedInFull() (recv, send int, err error) { return udpbuf.Request, udpbuf.Request, nil }
