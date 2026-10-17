package tunnel_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/tunnel"
)

// Every Tunnel connection has TCP keepalive of 5 s idle, a 3 s interval and 3
// probes, whichever side dialled and whichever accepted.
func TestTunnelConnectionsHaveTCPKeepaliveOnBothSides(t *testing.T) {
	privA, pubA, _ := keys.Generate()
	privB, pubB, _ := keys.Generate()
	addrA, addrB := netip.MustParseAddr("100.64.0.1"), netip.MustParseAddr("100.64.0.2")
	a, err := tunnel.New(tunnel.Config{Address: addrA, PrivateKey: privA, ListenPort: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := tunnel.New(tunnel.Config{Address: addrB, PrivateKey: privB})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	port, err := a.ListenPort()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.AddPeer(tunnel.Peer{PublicKey: pubB, AllowedIP: addrB}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddPeer(tunnel.Peer{PublicKey: pubA, AllowedIP: addrA, Endpoint: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(port))}); err != nil {
		t.Fatal(err)
	}

	ln, err := a.Net.ListenTCPAddrPort(netip.AddrPortFrom(addrA, 7000))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dialled, err := b.Net.DialContextTCPAddrPort(ctx, netip.AddrPortFrom(addrA, 7000))
	if err != nil {
		t.Fatal(err)
	}
	defer dialled.Close()
	accepted, err := ln.AcceptConn()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()

	for name, c := range map[string]interface {
		Keepalive() (bool, time.Duration, time.Duration, int, error)
	}{"dialled": dialled, "accepted": accepted} {
		on, idle, interval, probes, err := c.Keepalive()
		if err != nil {
			t.Fatal(err)
		}
		if !on || idle != 5*time.Second || interval != 3*time.Second || probes != 3 {
			t.Errorf("%s: keepalive on=%v idle=%v interval=%v probes=%d, want on, 5s, 3s, 3", name, on, idle, interval, probes)
		}
	}
}
