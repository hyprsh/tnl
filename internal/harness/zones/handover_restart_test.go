package zones

import (
	"testing"
	"time"

	. "github.com/hyprsh/tnl/internal/harness"
)

func TestARestartedOutpostWarnsAboutALineThatStillWaits(t *testing.T) {
	svc := NewHTTPService(t, EchoHost())
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, Photos, "http://"+svc.Addr(), "nas")
	lines := []OutpostLine{DeclaredLine("PHOTOS", Photos, "http://"+svc.Addr())}
	first := re.ConnectWith(t, "newbox", re.AddOutpost(t, "newbox"), ConnectOptions{Declaration: lines})
	waitWaiting(t, re, "newbox", Photos)
	first.Stop()

	olog := new(LockedBuf)
	reconnect(t, re, "newbox", first.StateDir, ConnectOptions{LogOut: olog, Declaration: lines})
	WaitFor(t, "the Outpost's warn", 10*time.Second, func() bool {
		return countLines(olog, "level=warn", "variable=TNL_ROUTE_PHOTOS", Photos, "held by Outpost nas") == 1
	})
}
