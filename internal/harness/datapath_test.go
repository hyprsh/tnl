package harness

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/wire"
)

func TestBytesFlowBothWaysAfterOK(t *testing.T) {
	svc := NewEchoService(t)
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{})

	conn, err := h.Edge.Dial(Ctx(t), o, wire.Header{Service: svc.Addr(), Hostname: "app.example.com", RequestID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("hello through the tunnel")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("hello through the tunnel"))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello through the tunnel" {
		t.Fatalf("got %q", got)
	}
}

func TestRepliesBeforeAnyRequestByte(t *testing.T) {
	// A Service that speaks only after the client does: the Edge must still get ok.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{})
	conn, err := h.Edge.Dial(Ctx(t), o, wire.Header{Service: ln.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func dialReason(t *testing.T, h *Harness, o *Outpost, service string) wire.Reason {
	t.Helper()
	_, err := h.Edge.Dial(Ctx(t), o, wire.Header{Service: service})
	var de *DialError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v, want a *DialError", err)
	}
	return de.Reason
}

func TestRefusedPortGivesRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{})
	if got := dialReason(t, h, o, addr); got != wire.ReasonRefused {
		t.Fatalf("reason = %v, want refused", got)
	}
}

func TestUnresolvableNameGivesDNS(t *testing.T) {
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{})
	if got := dialReason(t, h, o, "nonexistent.invalid:80"); got != wire.ReasonDNS {
		t.Fatalf("reason = %v, want dns", got)
	}
}

func TestBlackholedAddressGivesTimeout(t *testing.T) {
	// A dialer that never connects, with the dial budget cut to 200 ms. The
	// 5 s budget itself is a constant checked in the forwarder's own test.
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{
		DialTimeout: 200 * time.Millisecond,
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if got := dialReason(t, h, o, "192.0.2.1:80"); got != wire.ReasonTimeout {
		t.Fatalf("reason = %v, want timeout", got)
	}
}

func TestUnknownMarkerIsRefused(t *testing.T) {
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Version: "0.1.0"})
	rp := h.Edge.Raw(t, Ctx(t), o, []byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	if rp.Status != wire.StatusUnsupported || rp.Version != "0.1.0" {
		t.Fatalf("reply = %+v, want unsupported 0.1.0", rp)
	}
}

func TestUnknownFieldIsRefused(t *testing.T) {
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Version: "0.1.0"})
	raw := []byte{'t', 'n', 'l', 0, 1, 1, 3, 'a', ':', '1', 99, 1, 'x', 0}
	rp := h.Edge.Raw(t, Ctx(t), o, raw)
	if rp.Status != wire.StatusUnsupported || rp.Version != "0.1.0" {
		t.Fatalf("reply = %+v, want unsupported 0.1.0", rp)
	}
}

func TestSlowHeaderIsCutOff(t *testing.T) {
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{HeaderTimeout: 200 * time.Millisecond})
	conn, err := h.Edge.Net.DialContextTCPAddrPort(Ctx(t), o.DataAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("want the Outpost to close a connection that sends no header")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the Outpost never closed the connection")
	}
}

func TestTunnelHasNagleOff(t *testing.T) {
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{})
	for name, n := range map[string]interface{ TCPDelayEnabled() (bool, error) }{"edge": h.Edge.Net, "outpost": o.Net} {
		on, err := n.TCPDelayEnabled()
		if err != nil {
			t.Fatal(err)
		}
		if on {
			t.Errorf("%s: Nagle is on", name)
		}
	}
}
