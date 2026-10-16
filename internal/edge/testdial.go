package edge

import (
	"context"
	"sync/atomic"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

// lateTestDialer is the API's test dialer before the web side exists: the API
// starts first, and startWeb plugs the Caddy runtime in. With no web side
// there are no Visitors, so it dials nothing.
type lateTestDialer struct {
	d atomic.Pointer[routes.TestDialer]
}

func (l *lateTestDialer) set(d routes.TestDialer) { l.d.Store(&d) }

func (l *lateTestDialer) TestDial(ctx context.Context, r routes.Route) error {
	if d := l.d.Load(); d != nil {
		return (*d).TestDial(ctx, r)
	}
	return nil
}
