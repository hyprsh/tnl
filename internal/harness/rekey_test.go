package harness

import (
	"context"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/tunnel"
)

// A WireGuard rekey in a running Outpost is not a restart: the session id in
// its pings stays the same, and the Edge closes nothing.
func TestARekeyWithoutARestartClosesNothing(t *testing.T) {
	svc := eventService(t)
	logs := new(lockedBuf)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), LogOut: logs, LogLevel: slog.LevelInfo})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	get(t, re.Caddy, H1, "app.test", "/")
	next := openStream(t, re, "app.test")
	if !next() {
		t.Fatal("the stream did not start")
	}

	// Forget the Edge's session keys: the next packet handshakes afresh.
	edgeKey := re.Store.Snapshot().PrivateKey.Public()
	var endpoint string
	if err := o.Tunnel.EachStateLine(func(k, v string) {
		if k == "endpoint" {
			endpoint = v
		}
	}); err != nil || endpoint == "" {
		t.Fatalf("no endpoint for the Edge's peer: %v", err)
	}
	ep := netip.MustParseAddrPort(endpoint)
	if err := o.Tunnel.RemovePeer(edgeKey); err != nil {
		t.Fatal(err)
	}
	if err := o.Tunnel.AddPeer(tunnel.Peer{PublicKey: edgeKey, AllowedIP: addr.Edge, Endpoint: ep, KeepaliveSeconds: tunnel.OutpostKeepalive}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := o.Tunnel.WaitHandshake(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second) // ten pings with the new keys
	for i := 0; i < 2; i++ {
		if !next() {
			t.Fatal("the stream ended after a rekey")
		}
	}
	if strings.Contains(logs.String(), "outpost restarted") {
		t.Fatalf("the Edge took a rekey for a restart:\n%s", logs)
	}
}
