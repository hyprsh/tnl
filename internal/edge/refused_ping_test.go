package edge

import (
	"bytes"
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/tunnel"
	"github.com/hyprsh/tnl/internal/wire"
)

// A later Outpost's ping has a tag this Edge refuses. Its version still decides.
func TestRefusedPingIsJudgedByItsVersion(t *testing.T) {
	l := liveness.New(liveness.Config{EdgeVersion: "0.2.0"})
	ping := func(version string) []byte {
		b := []byte{'t', 'n', 'l', 1, 1, 1, byte(len(version))}
		b = append(b, version...)
		return append(b, 99, 1, 7, 0) // an unknown tag, then the end
	}
	if p, ok := judgeRefusedPing(l, ping("0.3.0")); !ok || p.Version != "0.3.0" {
		t.Errorf("a newer minor: %+v, %v", p, ok)
	}
	if _, ok := judgeRefusedPing(l, ping("0.2.5")); ok {
		t.Error("a ping inside the window was taken")
	}
	if _, ok := judgeRefusedPing(l, []byte{'t', 'n', 'l', 1, 9, 0}); ok {
		t.Error("a ping of an unknown format was taken")
	}
}

// The Edge's real ping path: a ping with an unknown tag from a version outside
// the window makes the Outpost Incompatible, and is answered with the window
// flag at 0 and nothing else.
func TestRefusedPingOutsideTheWindowIsAnsweredAsOutside(t *testing.T) {
	dir := shortDir(t)
	r, _, _ := startEdge(t, dir)
	tok := addOutpost(t, dir, "laptop")

	tun, err := tunnel.New(tunnel.Config{Address: tok.TunnelAddr, PrivateKey: tok.OneTimeKey})
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	err = tun.AddPeer(tunnel.Peer{
		PublicKey: tok.EdgeKey, AllowedIP: addr.Edge,
		Endpoint:         netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(r.WireGuardPort)),
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

	conn, err := tun.Net.DialUDPAddrPort(netip.AddrPortFrom(tok.TunnelAddr, wire.PingPort), netip.AddrPortFrom(addr.Edge, wire.PingPort))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Version 0.1.0 is outside the window of v9.9.9; tag 99 is unknown.
	ping := []byte{'t', 'n', 'l', 1, 1, 1, 5}
	ping = append(ping, "0.1.0"...)
	ping = append(ping, 99, 1, 7, 0)
	if _, err := wire.ParsePing(ping); err == nil {
		t.Fatal("the ping should carry a tag this build refuses")
	}
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(ping); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal("no answer to the ping: ", err)
	}
	pong, err := wire.ParsePong(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if pong.Inside || pong.Declaration != nil {
		t.Errorf("answer %+v, want the window flag at 0 and nothing else", pong)
	}
	want, _ := wire.AppendPong(nil, wire.Pong{Version: "v9.9.9"})
	if !bytes.Equal(buf[:n], want) {
		t.Errorf("answer bytes %x, want %x", buf[:n], want)
	}
	if got := r.Liveness.State("laptop"); got != liveness.Incompatible {
		t.Errorf("the Outpost is %v, want Incompatible", got)
	}
}
