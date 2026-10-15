// Package harness is the in-process test harness every later ticket extends.
// It brings up an Edge and Outposts, each on userspace WireGuard over loopback
// UDP, with no root, TUN device or kernel module. Later tickets add the Caddy
// side, Pebble and the HTTP clients here.
//
// It is a non-test package so other packages' tests can import it. cmd/tnl
// must never import it.
package harness

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/forwarder"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/tunnel"
	"github.com/hyprsh/tnl/internal/tunnel/netstack"
	"github.com/hyprsh/tnl/internal/wire"
)

// Harness is one Edge and the Outposts added to it.
type Harness struct {
	Edge *Edge

	edgePub  keys.Key
	outposts int
}

// Edge is the Edge end of the Tunnel.
type Edge struct {
	Tunnel *tunnel.Tunnel
	Net    *netstack.Net
	Port   int // the UDP port WireGuard listens on, on 127.0.0.1
}

// Outpost is one Outpost: its Tunnel end and its forwarder.
type Outpost struct {
	Addr      netip.Addr
	Tunnel    *tunnel.Tunnel
	Net       *netstack.Net
	Forwarder *forwarder.Forwarder
}

// OutpostOptions tunes an Outpost; the zero value is a normal one.
type OutpostOptions struct {
	Version       string
	Dialer        forwarder.Dialer
	DialTimeout   time.Duration
	HeaderTimeout time.Duration
}

// New starts an Edge. Everything stops when the test ends.
func New(t testing.TB) *Harness {
	t.Helper()
	priv, pub, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	tun, err := tunnel.New(tunnel.Config{Address: addr.Edge, PrivateKey: priv})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tun.Close)
	port, err := tun.ListenPort()
	if err != nil {
		t.Fatal(err)
	}
	return &Harness{Edge: &Edge{Tunnel: tun, Net: tun.Net, Port: port}, edgePub: pub}
}

// AddOutpost starts an Outpost with the next Tunnel address, peers it with the
// Edge and waits for the first handshake.
func (h *Harness) AddOutpost(t testing.TB, opts OutpostOptions) *Outpost {
	t.Helper()
	priv, pub, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	a := addr.Outpost(h.outposts)
	h.outposts++

	if err := h.Edge.Tunnel.AddPeer(tunnel.Peer{PublicKey: pub, AllowedIP: a}); err != nil {
		t.Fatal(err)
	}
	tun, err := tunnel.New(tunnel.Config{Address: a, PrivateKey: priv})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tun.Close)
	err = tun.AddPeer(tunnel.Peer{
		PublicKey:        h.edgePub,
		AllowedIP:        addr.Edge,
		Endpoint:         netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(h.Edge.Port)),
		KeepaliveSeconds: tunnel.OutpostKeepalive,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The Edge learns the Outpost's endpoint from the Outpost's first packet, so
	// wait for the handshake before the Edge dials.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tun.WaitHandshake(ctx); err != nil {
		t.Fatal(err)
	}

	ln, err := tun.Net.ListenTCPAddrPort(netip.AddrPortFrom(a, wire.DataPort))
	if err != nil {
		t.Fatal(err)
	}
	version := opts.Version
	if version == "" {
		version = "0.0.0-test"
	}
	fw := &forwarder.Forwarder{Version: version, Dialer: opts.Dialer, DialTimeout: opts.DialTimeout, HeaderTimeout: opts.HeaderTimeout}
	done := make(chan struct{})
	go func() { defer close(done); fw.Serve(ln) }()
	t.Cleanup(func() { ln.Close(); <-done })

	return &Outpost{Addr: a, Tunnel: tun, Net: tun.Net, Forwarder: fw}
}

// DataAddr is where the Edge reaches the Outpost's forwarder.
func (o *Outpost) DataAddr() netip.AddrPort { return netip.AddrPortFrom(o.Addr, wire.DataPort) }

// DialError is a dial error reply from the Outpost.
type DialError struct{ Reason wire.Reason }

func (e *DialError) Error() string { return "outpost could not dial the Service: " + e.Reason.String() }

// UnsupportedError is an unsupported reply from the Outpost.
type UnsupportedError struct{ Version string }

func (e *UnsupportedError) Error() string {
	return "outpost " + e.Version + " refused the tnl header"
}

// Dial opens a Tunnel connection to o, sends the tnl header and reads the
// reply. On ok it returns the connection, ready for request bytes; otherwise it
// returns a *DialError or *UnsupportedError.
func (e *Edge) Dial(ctx context.Context, o *Outpost, h wire.Header) (net.Conn, error) {
	c, err := e.Net.DialContextTCPAddrPort(ctx, o.DataAddr())
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	if err := wire.WriteHeader(c, h); err != nil {
		c.Close()
		return nil, err
	}
	rp, err := wire.ReadReply(c)
	if err != nil {
		c.Close()
		return nil, err
	}
	c.SetDeadline(time.Time{})
	switch rp.Status {
	case wire.StatusOK:
		return c, nil
	case wire.StatusDialError:
		c.Close()
		return nil, &DialError{Reason: rp.Reason}
	default:
		c.Close()
		return nil, &UnsupportedError{Version: rp.Version}
	}
}

// Raw sends raw bytes in place of a header and returns the reply.
func (e *Edge) Raw(t testing.TB, ctx context.Context, o *Outpost, b []byte) wire.Reply {
	t.Helper()
	c, err := e.Net.DialContextTCPAddrPort(ctx, o.DataAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	if _, err := c.Write(b); err != nil {
		t.Fatal(err)
	}
	rp, err := wire.ReadReply(c)
	if err != nil {
		t.Fatal(fmt.Errorf("read reply: %w", err))
	}
	return rp
}
