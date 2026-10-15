package harness

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/edge"
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
}

// harnessVersion is the version the real Edge, the Outposts and the API client
// all report, so the CLI-style exact-version check passes.
const harnessVersion = "v0.0.0-test"

// StartEdge runs edge.Run on a temporary state directory, WireGuard on a free
// loopback-reachable port and --address 127.0.0.1. It stops when the test ends.
func StartEdge(t testing.TB) *RealEdge {
	t.Helper()
	// Unix socket paths are short on macOS, so keep the directory name short.
	dir, err := os.MkdirTemp("", "tnl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	ctx, cancel := context.WithCancel(context.Background())
	up := make(chan *edge.Running, 1)
	done := make(chan error, 1)
	go func() {
		done <- edge.Run(ctx, edge.Config{
			Address: "127.0.0.1", StateDir: dir, Version: harnessVersion, Build: "test",
			Log:     logx.New(logx.Options{Level: slog.LevelWarn, Out: testWriter{t}}),
			Started: func(r *edge.Running) { up <- r },
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	var r *edge.Running
	select {
	case r = <-up:
	case err := <-done:
		t.Fatalf("edge.Run: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the Edge did not start")
	}
	return &RealEdge{
		Edge:     &Edge{Routes: new(routes.Mem), Outposts: new(routes.MemOutposts), Tunnel: r.Tunnel, Net: r.Tunnel.Net, Port: r.WireGuardPort},
		StateDir: dir,
		API:      &api.Client{StateDir: dir, Version: harnessVersion},
		Store:    r.Store,
	}
}

// AddOutpost calls `outpost add` on the running Edge and returns the join token.
func (e *RealEdge) AddOutpost(t testing.TB, name string) string {
	t.Helper()
	resp, err := api.Call[api.OutpostAddResponse](context.Background(), e.API, http.MethodPost, api.PathOutposts, api.OutpostAddRequest{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Token
}

// JoinedOutpost is an Outpost started with outpost.Run.
type JoinedOutpost struct {
	*Outpost
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
	ctx, cancel := context.WithCancel(context.Background())
	up := make(chan *outpost.Running, 1)
	done := make(chan error, 1)
	go func() {
		done <- outpost.Run(ctx, outpost.Config{
			Token: tok, StateDir: stateDir, Version: harnessVersion, Build: "test",
			Log:     logx.New(logx.Options{Level: slog.LevelWarn, Out: testWriter{t}}),
			Started: func(r *outpost.Running) { up <- r },
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
		t.Fatalf("outpost.Run: %v", err)
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
	return &JoinedOutpost{Outpost: o, Token: tok, StateDir: stateDir, Stop: func() error { stopped = true; return stop() }}
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
