package edge

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/token"
	"github.com/hyprsh/tnl/internal/tunnel"
)

// startEdge runs the Edge on WireGuard port 0 in dir and returns once it is up.
func startEdge(t *testing.T, dir string) (running *Running, log *syncBuf, stop func()) {
	t.Helper()
	log = &syncBuf{}
	lg := logx.New(logx.Options{Level: slog.LevelInfo, Out: log})
	ctx, cancel := context.WithCancel(context.Background())
	up := make(chan *Running, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Address: "127.0.0.1", StateDir: dir, Version: "v9.9.9", Build: "abc", Log: lg, Started: func(r *Running) { up <- r }})
	}()
	select {
	case running = <-up:
	case err := <-done:
		t.Fatalf("Run returned: %v\n%s", err, log)
	case <-time.After(5 * time.Second):
		t.Fatal("the Edge did not start")
	}
	stop = func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { cancel() })
	return running, log, stop
}

// joinWith brings up an Outpost's end of the Tunnel from a token and waits for
// the handshake, which proves the Edge has the token's key as a peer.
func joinWith(t *testing.T, tok token.Token, port int) {
	t.Helper()
	tun, err := tunnel.New(tunnel.Config{Address: tok.TunnelAddr, PrivateKey: tok.OneTimeKey})
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	err = tun.AddPeer(tunnel.Peer{
		PublicKey: tok.EdgeKey, AllowedIP: netip.MustParseAddr("100.64.0.1"),
		Endpoint:         netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(port)),
		KeepaliveSeconds: tunnel.OutpostKeepalive,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tun.WaitHandshake(ctx); err != nil {
		t.Fatal(err)
	}
}

func addOutpost(t *testing.T, dir, name string) token.Token {
	t.Helper()
	c := &api.Client{StateDir: dir, Version: "v9.9.9"}
	resp, err := api.Call[api.OutpostAddResponse](context.Background(), c, http.MethodPost, api.PathOutposts, api.OutpostAddRequest{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := token.Decode(resp.Token)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestOutpostAddedWhileRunningJoinsWithoutARestart(t *testing.T) {
	dir := shortDir(t)
	r, log, _ := startEdge(t, dir)
	if !strings.Contains(log.String(), "wireguard_port=") {
		t.Errorf("start-up log lacks the WireGuard port:\n%s", log)
	}
	tok := addOutpost(t, dir, "laptop")
	if tok.Port != r.WireGuardPort || tok.Port == 0 {
		t.Fatalf("token port %d, Edge port %d", tok.Port, r.WireGuardPort)
	}
	joinWith(t, tok, r.WireGuardPort)
}

func TestStartBuildsPeersFromState(t *testing.T) {
	dir := shortDir(t)
	_, _, stop := startEdge(t, dir)
	tok := addOutpost(t, dir, "laptop")
	stop()

	r, _, _ := startEdge(t, dir)
	joinWith(t, tok, r.WireGuardPort)
	if keys.Key(tok.EdgeKey) != r.Store.Snapshot().PrivateKey.Public() {
		t.Error("the Edge's key changed across a restart")
	}
}
