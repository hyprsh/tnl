package caddyx

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/hyprsh/tnl/internal/wire"
)

// A dial the Service refused or couldn't resolve may succeed soon (a deploy, a
// restart), so the Edge asks again every retryInterval until retryBudget has
// passed since the first attempt. Retrying is safe because no request byte has
// been sent. Variables so tests of the loop itself can shrink them.
var (
	retryInterval = 250 * time.Millisecond
	retryBudget   = 5 * time.Second
)

// retryable says whether a dial error is one worth another attempt: a `refused`
// or `dns` reply. A `timeout` already took the Outpost's 5 s, and `allowlist`
// won't change.
func retryable(err error) bool {
	var re *ReplyError
	if !errors.As(err, &re) || re.Reply.Status != wire.StatusDialError {
		return false
	}
	return re.Reply.Reason == wire.ReasonRefused || re.Reply.Reason == wire.ReasonDNS
}

// dialOutpost is dialOutpostOnce with the retries of spec section 6, step 4.
// When they run out, the last attempt's error is returned.
func (rt *Runtime) dialOutpost(ctx context.Context, addr netip.AddrPort, outpost string, h wire.Header) (net.Conn, error) {
	start := time.Now()
	for {
		c, err := rt.dialOutpostOnce(ctx, addr, outpost, h)
		if err == nil || !retryable(err) || time.Since(start) >= retryBudget {
			return c, err
		}
		select {
		case <-time.After(retryInterval):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
