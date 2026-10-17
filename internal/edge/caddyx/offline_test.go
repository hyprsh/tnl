package caddyx

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/edge/routes"
	"github.com/hyprsh/tnl/internal/wire"
)

type fixedState liveness.State

func (f fixedState) State(string) liveness.State { return liveness.State(f) }

// transportFor is a Transport whose Route goes through Outpost "home", with the
// request already carrying it the way the Router leaves it.
func transportFor(rt *Runtime) (*Transport, *http.Request) {
	rt.Table = new(routes.Mem)
	outposts := new(routes.MemOutposts)
	outposts.Set("home", netip.MustParseAddrPort("100.64.0.2:7000"))
	rt.Outposts = outposts
	vars := map[string]any{
		varRoute:     routes.Route{Hostname: "app.test", Outpost: "home", Service: "s:1"},
		varRequestID: "r1",
	}
	req, _ := http.NewRequestWithContext(context.WithValue(context.Background(), caddyhttp.VarsCtxKey, vars), "GET", "http://app.test/", nil)
	req.Host = "app.test"
	return &Transport{rt: rt}, req
}

func TestAnOfflineOutpostsRouteGets503WithoutADial(t *testing.T) {
	dialed := false
	tr, req := transportFor(&Runtime{
		Liveness: fixedState(liveness.Offline),
		Dialer:   func(context.Context, netip.AddrPort) (net.Conn, error) { dialed = true; return nil, net.ErrClosed },
	})
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable || dialed {
		t.Fatalf("status %d, dialed %v", resp.StatusCode, dialed)
	}
}

func TestAnOutpostTheEdgeHasNotHeardFromYetIsDialled(t *testing.T) {
	dialed := false
	tr, req := transportFor(&Runtime{
		Liveness: fixedState(liveness.Unknown),
		Dialer:   func(context.Context, netip.AddrPort) (net.Conn, error) { dialed = true; return nil, net.ErrClosed },
	})
	tr.RoundTrip(req)
	if !dialed {
		t.Fatal("an Outpost that joined a moment ago was answered without a dial")
	}
}

func TestAnOnlineOutpostWithNoTunnelGets503AfterTheWait(t *testing.T) {
	old := tunnelWait
	tunnelWait = 30 * time.Millisecond
	t.Cleanup(func() { tunnelWait = old })
	tr, req := transportFor(&Runtime{
		Liveness: fixedState(liveness.Online),
		Dialer: func(ctx context.Context, _ netip.AddrPort) (net.Conn, error) {
			<-ctx.Done() // WireGuard has no session: the connect just hangs
			return nil, ctx.Err()
		},
	})
	start := time.Now()
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if time.Since(start) < tunnelWait {
		t.Fatal("answered before the wait was over")
	}
}

func TestAVisitorWhoLeavesDuringTheWaitIsNotA503(t *testing.T) {
	tr, req := transportFor(&Runtime{
		Liveness: fixedState(liveness.Online),
		Dialer: func(ctx context.Context, _ netip.AddrPort) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	time.AfterFunc(20*time.Millisecond, cancel)
	if resp, err := tr.RoundTrip(req); err == nil {
		t.Fatalf("got a response %d for a Visitor who left", resp.StatusCode)
	}
}

// A connect that started while the Tunnel was down is not answered when the
// Tunnel returns: TCP's SYN retransmits back off (1, 2, 4 s ...), so the next
// one can fall after the wait is over. The transport therefore dials again every
// tunnelAttempt, and a dial that starts after the Tunnel is back gets through.
func TestARequestWaitingForTheTunnelGoesThroughWhenItReturns(t *testing.T) {
	oldW, oldA := tunnelWait, tunnelAttempt
	tunnelWait, tunnelAttempt = 2*time.Second, 40*time.Millisecond
	t.Cleanup(func() { tunnelWait, tunnelAttempt = oldW, oldA })
	back := time.Now().Add(300 * time.Millisecond)
	rt := &Runtime{
		Liveness: fixedState(liveness.Online),
		Dialer: func(ctx context.Context, _ netip.AddrPort) (net.Conn, error) {
			if time.Now().Before(back) {
				<-ctx.Done() // this connect was started too early to ever be answered
				return nil, ctx.Err()
			}
			c, outpost := net.Pipe()
			go func() {
				defer outpost.Close()
				if _, err := wire.ReadHeader(outpost); err == nil {
					wire.WriteReply(outpost, wire.Reply{Status: wire.StatusOK})
					if _, err := http.ReadRequest(bufio.NewReader(outpost)); err == nil {
						io.WriteString(outpost, "HTTP/1.1 204 No Content\r\n\r\n")
					}
				}
			}()
			return c, nil
		},
	}
	tr, req := transportFor(rt)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d, want the Service's 204", resp.StatusCode)
	}
}
