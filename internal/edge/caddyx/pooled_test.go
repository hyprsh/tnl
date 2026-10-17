package caddyx

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/wire"
)

// The spec's bound for a request sent on a pooled connection whose Tunnel went
// down: no answer after tunnelAttempt starts a connect to the Outpost, and a
// Tunnel that stays down for tunnelWait ends the request with 503 "tunnel not
// up". So it ends within tunnelAttempt + tunnelWait, however long the Outpost's
// Offline window is.
func TestARequestOnAPooledConnectionIsBoundedByTheAttemptPlusTheWait(t *testing.T) {
	oldW, oldA := tunnelWait, tunnelAttempt
	tunnelWait, tunnelAttempt = 400*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { tunnelWait, tunnelAttempt = oldW, oldA })
	var dials atomic.Int32
	rt := &Runtime{
		Liveness: fixedState(liveness.Online),
		Dialer: func(ctx context.Context, _ netip.AddrPort) (net.Conn, error) {
			if dials.Add(1) > 1 {
				<-ctx.Done() // the Tunnel is down: the connect hangs
				return nil, ctx.Err()
			}
			c, outpost := net.Pipe()
			go func() {
				defer outpost.Close()
				if _, err := wire.ReadHeader(outpost); err != nil {
					return
				}
				wire.WriteReply(outpost, wire.Reply{Status: wire.StatusOK})
				br := bufio.NewReader(outpost)
				if _, err := http.ReadRequest(br); err != nil {
					return
				}
				io.WriteString(outpost, "HTTP/1.1 204 No Content\r\n\r\n")
				// The pooled connection now reads a request and never answers it.
				http.ReadRequest(br)
				io.Copy(io.Discard, br)
			}()
			return c, nil
		},
	}
	tr, req := transportFor(rt)
	first, err := tr.RoundTrip(req.Clone(req.Context()))
	if err != nil || first.StatusCode != http.StatusNoContent {
		t.Fatalf("first request: %v %v", first, err)
	}
	first.Body.Close()

	start := time.Now()
	resp, err := tr.RoundTrip(req.Clone(req.Context()))
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", resp.StatusCode)
	}
	if dials.Load() < 2 {
		t.Fatal("the second request did not use a pooled connection and a watch")
	}
	if want := tunnelAttempt + tunnelWait; took < want || took > want+300*time.Millisecond {
		t.Fatalf("ended after %v, want %v (attempt plus wait)", took, want)
	}
}
