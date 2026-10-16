package edge

import (
	"context"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

// lateTestDialer is the API's test dialer before the web side exists: the API
// starts first, and startWeb plugs the Caddy runtime in. With no web side
// there are no Visitors, so it dials nothing.
type lateTestDialer struct{ late[routes.TestDialer] }

type noTestDial struct{}

func (noTestDial) TestDial(context.Context, routes.Route) error { return nil }

func (l *lateTestDialer) TestDial(ctx context.Context, r routes.Route) error {
	return l.get(noTestDial{}).TestDial(ctx, r)
}
