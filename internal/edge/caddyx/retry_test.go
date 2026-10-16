package caddyx

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/wire"
)

// replyingRuntime answers each dial with the next reply in replies, the last one
// repeating, and counts the dials.
func replyingRuntime(replies []wire.Reply, dials *int) *Runtime {
	return &Runtime{Dialer: func(ctx context.Context, _ netip.AddrPort) (net.Conn, error) {
		rp := replies[min(*dials, len(replies)-1)]
		*dials++
		c, outpost := net.Pipe()
		go func() {
			defer outpost.Close()
			if _, err := wire.ReadHeader(outpost); err != nil {
				return
			}
			wire.WriteReply(outpost, rp)
		}()
		return c, nil
	}}
}

func shrinkRetries(t *testing.T) {
	t.Helper()
	oldI, oldB := retryInterval, retryBudget
	retryInterval, retryBudget = time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { retryInterval, retryBudget = oldI, oldB })
}

func dialReply(r wire.Reason) wire.Reply {
	return wire.Reply{Status: wire.StatusDialError, Reason: r}
}

func TestDialRetriesRefusedAndDNSUntilTheBudgetIsSpent(t *testing.T) {
	shrinkRetries(t)
	for _, r := range []wire.Reason{wire.ReasonRefused, wire.ReasonDNS} {
		dials := 0
		rt := replyingRuntime([]wire.Reply{dialReply(r)}, &dials)
		_, err := rt.dialOutpost(context.Background(), netip.AddrPort{}, "o", wire.Header{Service: "x:1", Hostname: "x"})
		var re *ReplyError
		if !errors.As(err, &re) || re.Reply.Reason != r {
			t.Fatalf("%v: got %v", r, err)
		}
		if dials < 5 {
			t.Errorf("%v: %d attempts, want retries", r, dials)
		}
	}
}

func TestDialSucceedsWhenARetryDoes(t *testing.T) {
	shrinkRetries(t)
	dials := 0
	rt := replyingRuntime([]wire.Reply{dialReply(wire.ReasonRefused), dialReply(wire.ReasonRefused), {Status: wire.StatusOK}}, &dials)
	c, err := rt.dialOutpost(context.Background(), netip.AddrPort{}, "o", wire.Header{Service: "x:1", Hostname: "x"})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if dials != 3 {
		t.Errorf("%d attempts, want 3", dials)
	}
}

func TestDialDoesNotRetryTimeoutOrAllowlist(t *testing.T) {
	shrinkRetries(t)
	for _, r := range []wire.Reason{wire.ReasonTimeout, wire.ReasonAllowlist} {
		dials := 0
		rt := replyingRuntime([]wire.Reply{dialReply(r)}, &dials)
		_, err := rt.dialOutpost(context.Background(), netip.AddrPort{}, "o", wire.Header{Service: "x:1", Hostname: "x"})
		var re *ReplyError
		if !errors.As(err, &re) || dials != 1 {
			t.Errorf("%v: %d attempts, err %v; want 1 and a ReplyError", r, dials, err)
		}
	}
}

func TestDialRetryStopsWhenTheContextEnds(t *testing.T) {
	oldI, oldB := retryInterval, retryBudget
	retryInterval, retryBudget = 10*time.Millisecond, time.Minute
	t.Cleanup(func() { retryInterval, retryBudget = oldI, oldB })
	dials := 0
	rt := replyingRuntime([]wire.Reply{dialReply(wire.ReasonRefused)}, &dials)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := rt.dialOutpost(ctx, netip.AddrPort{}, "o", wire.Header{Service: "x:1", Hostname: "x"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want the context's error", err)
	}
}

// A context that ends while the header exchange is waiting closes the
// connection under it; the error is still the context's, not the pipe's.
func TestDialExchangeInterruptedByTheContextReturnsItsError(t *testing.T) {
	rt := &Runtime{Dialer: func(context.Context, netip.AddrPort) (net.Conn, error) {
		c, outpost := net.Pipe()
		t.Cleanup(func() { outpost.Close() })
		return c, nil // the Outpost never answers
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := rt.dialOutpostOnce(ctx, netip.AddrPort{}, "o", wire.Header{Service: "x:1", Hostname: "x"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want the context's error", err)
	}
}
