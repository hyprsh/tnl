package outpost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/token"
	"github.com/hyprsh/tnl/internal/tunnel"
	"github.com/hyprsh/tnl/internal/wire"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// fakeEdge is just the Edge's end of the Tunnel, with one Outpost as a peer,
// and a stub of the join path: it swaps the peer's key as the real Edge does,
// and answers with the name "laptop". The real exchange is tested in the
// harness.
type fakeEdge struct {
	tun     *tunnel.Tunnel
	port    int
	key     keys.Key // private
	oneTime keys.Key // the peer's public key at the start
	joins   chan api.JoinRequest
}

func newFakeEdge(t *testing.T, edgeKey keys.Key, port int, oneTime keys.Key) *fakeEdge {
	t.Helper()
	tun, err := tunnel.New(tunnel.Config{Address: addr.Edge, PrivateKey: edgeKey, ListenPort: port})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tun.Close)
	if err := tun.AddPeer(tunnel.Peer{PublicKey: oneTime.Public(), AllowedIP: addr.Outpost(0)}); err != nil {
		t.Fatal(err)
	}
	port, err = tun.ListenPort()
	if err != nil {
		t.Fatal(err)
	}
	e := &fakeEdge{tun: tun, port: port, key: edgeKey, oneTime: oneTime.Public(), joins: make(chan api.JoinRequest, 4)}
	ln, err := tun.Net.ListenTCPAddrPort(netip.AddrPortFrom(addr.Edge, api.TunnelPort))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+api.PathJoin, func(w http.ResponseWriter, r *http.Request) {
		var req api.JoinRequest
		json.NewDecoder(r.Body).Decode(&req)
		tun.AddPeer(tunnel.Peer{PublicKey: req.PublicKey}) // can handshake, owns no address yet
		w.Header().Set(api.VersionHeader, "v9.9.9")
		b, _ := json.Marshal(api.JoinResponse{Name: "laptop", EdgeVersion: "v9.9.9"})
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.Write(b)
		w.(http.Flusher).Flush()
		e.joins <- req
		go func() {
			time.Sleep(150 * time.Millisecond)
			tun.AddPeer(tunnel.Peer{PublicKey: req.PublicKey, AllowedIP: addr.Outpost(0)})
			tun.RemovePeer(e.oneTime)
		}()
	})
	mux.HandleFunc("GET "+api.PathInfo, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(api.VersionHeader, "v9.9.9")
		json.NewEncoder(w).Encode(api.Info{Role: api.RoleEdge, Version: "v9.9.9", Build: "x"})
	})
	hs := &http.Server{Handler: mux}
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close() })
	return e
}

func newToken(t *testing.T, address string) (token.Token, keys.Key) {
	t.Helper()
	edgeKey, _, _ := keys.Generate()
	oneTime, _, _ := keys.Generate()
	return token.Token{Address: address, EdgeKey: edgeKey.Public(), OneTimeKey: oneTime, TunnelAddr: addr.Outpost(0), EdgeVersion: "0.1.0"}, edgeKey
}

type run struct {
	log  *syncBuf
	up   *Running
	stop func() error
	done chan error
}

func start(t *testing.T, cfg Config) (*run, error) {
	t.Helper()
	r := &run{log: &syncBuf{}, done: make(chan error, 1)}
	cfg.Log = logx.New(logx.Options{Level: slog.LevelInfo, Out: r.log})
	cfg.Version, cfg.Build = "v9.9.9", "abc123"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	up := make(chan *Running, 1)
	cfg.Started = func(x *Running) { up <- x }
	go func() { r.done <- Run(ctx, cfg) }()
	select {
	case r.up = <-up:
	case err := <-r.done:
		return r, err
	case <-time.After(10 * time.Second):
		t.Fatal("the Outpost did not start")
	}
	r.stop = func() error { cancel(); return <-r.done }
	return r, nil
}

func handshake(t *testing.T, e *fakeEdge) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.tun.WaitHandshake(ctx); err != nil {
		t.Fatal(err)
	}
}

func resolveTo(ip string) func(context.Context, string) (netip.Addr, error) {
	return func(_ context.Context, host string) (netip.Addr, error) { return netip.MustParseAddr(ip), nil }
}

func TestJoinBringsTheTunnelUpAndWritesTheState(t *testing.T) {
	tok, edgeKey := newToken(t, "127.0.0.1")
	edge := newFakeEdge(t, edgeKey, 0, tok.OneTimeKey)
	tok.Port = edge.port
	dir := shortStateDir(t)

	r, err := start(t, Config{Token: tok.Encode(), StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	handshake(t, edge)

	// The forwarder listens on the Outpost's Tunnel address at the data port.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := edge.tun.Net.DialContextTCPAddrPort(ctx, netip.AddrPortFrom(tok.TunnelAddr, wire.DataPort))
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	log := r.log.String()
	for _, want := range []string{"version=v9.9.9", "tunnel_address=100.64.0.2", "edge_address=127.0.0.1", "state_dir=" + dir} {
		if !strings.Contains(log, want) {
			t.Errorf("start-up log lacks %q:\n%s", want, log)
		}
	}

	di, err := os.Stat(dir)
	if err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("dir: %v %v", di, err)
	}
	fi, err := os.Stat(filepath.Join(dir, "state.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", fi, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	oneTime, _ := tok.OneTimeKey.MarshalText()
	if got["private_key"] == string(oneTime) || got["private_key"] == "" {
		t.Errorf("the Outpost kept the token's key: %v", got["private_key"])
	}
	delete(got, "private_key")
	edgePub, _ := tok.EdgeKey.MarshalText()
	want := map[string]any{
		"version": 2.0, "name": "laptop", "address": "100.64.0.2",
		"edge_address": "127.0.0.1", "edge_port": float64(edge.port), "edge_public_key": string(edgePub),
		"token_sha256": token.Hash(tok.Encode()),
	}
	if len(got) != len(want) {
		t.Errorf("state.json = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("state.json %s = %v, want %v", k, got[k], v)
		}
	}
	if err := r.stop(); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownMarkerIsRefusedBeforeTheStateIsTouched(t *testing.T) {
	dir := shortStateDir(t)
	_, err := start(t, Config{Token: "tnl2.whatever", StateDir: dir})
	var ce *ConfigError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), "newer tnl") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the state directory was touched: %v", err)
	}
}

func TestNoTokenAndNoStateIsAConfigError(t *testing.T) {
	_, err := start(t, Config{StateDir: shortStateDir(t)})
	var ce *ConfigError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), "--token") {
		t.Fatalf("err = %v", err)
	}
}

func TestRestartWithNoTokenReconnectsFromState(t *testing.T) {
	tok, edgeKey := newToken(t, "127.0.0.1")
	edge := newFakeEdge(t, edgeKey, 0, tok.OneTimeKey)
	tok.Port = edge.port
	dir := shortStateDir(t)
	r, err := start(t, Config{Token: tok.Encode(), StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	handshake(t, edge)
	if err := r.stop(); err != nil {
		t.Fatal(err)
	}
	edge.tun.Close()

	// The Edge comes back on the same port, knowing the Outpost's own key; the
	// Outpost has only its state.
	own := ownKey(t, dir)
	edge2 := newFakeEdge(t, edgeKey, edge.port, own)
	for name, token := range map[string]string{"no token": "", "the same token": tok.Encode()} {
		r, err = start(t, Config{Token: token, StateDir: dir})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		handshake(t, edge2)
		if err := r.stop(); err != nil {
			t.Fatal(err)
		}
		edge2.tun.Close()
		edge2 = newFakeEdge(t, edgeKey, edge.port, own)
	}
}

func TestADifferentTokenJoinsAgainAndReplacesTheState(t *testing.T) {
	tok, edgeKey := newToken(t, "127.0.0.1")
	edge := newFakeEdge(t, edgeKey, 0, tok.OneTimeKey)
	tok.Port = edge.port
	dir := shortStateDir(t)
	r, err := start(t, Config{Token: tok.Encode(), StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	r.stop()
	before := ownKey(t, dir)

	other, otherEdgeKey := newToken(t, "127.0.0.1")
	otherEdge := newFakeEdge(t, otherEdgeKey, 0, other.OneTimeKey)
	other.Port = otherEdge.port
	r, err = start(t, Config{Token: other.Encode(), StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer r.stop()
	st, err := loadState(dir)
	if err != nil || st.TokenSHA256 != token.Hash(other.Encode()) || st.EdgePort != otherEdge.port || st.PrivateKey == before {
		t.Errorf("the state was not replaced: %+v, %v", st, err)
	}
}

func TestATokenTheEdgeRefusesLeavesTheStateUntouched(t *testing.T) {
	tok, _ := newToken(t, "127.0.0.1")
	// No Edge answers on this port, so the join times out.
	tok.Port = 1
	dir := shortStateDir(t)
	_, err := start(t, Config{Token: tok.Encode(), StateDir: dir, JoinTimeout: 300 * time.Millisecond})
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the state directory was touched: %v", err)
	}
}

// ownKey is the private key a joined Outpost saved.
func ownKey(t *testing.T, dir string) keys.Key {
	t.Helper()
	st, err := loadState(dir)
	if err != nil || st == nil {
		t.Fatalf("state: %v, %v", st, err)
	}
	return st.PrivateKey
}

func TestTheEdgeAddressIsResolvedByTheOutpost(t *testing.T) {
	tok, edgeKey := newToken(t, "edge.example.test")
	edge := newFakeEdge(t, edgeKey, 0, tok.OneTimeKey)
	tok.Port = edge.port
	r, err := start(t, Config{Token: tok.Encode(), StateDir: shortStateDir(t), Resolve: resolveTo("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	handshake(t, edge)
	if !strings.Contains(r.log.String(), "edge_address=edge.example.test") {
		t.Errorf("log: %s", r.log)
	}
	r.stop()
}

func TestAnUnresolvableEdgeAddressLeavesNoState(t *testing.T) {
	tok, _ := newToken(t, "no-such-host.invalid")
	tok.Port = 51820
	dir := shortStateDir(t)
	_, err := start(t, Config{Token: tok.Encode(), StateDir: dir})
	if err == nil || !strings.Contains(err.Error(), "no-such-host.invalid") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Errorf("state written: %v", err)
	}
}

// shortStateDir is a state directory whose tnl.sock path fits a Unix socket.
func shortStateDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "tnlo")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return filepath.Join(d, "state")
}
