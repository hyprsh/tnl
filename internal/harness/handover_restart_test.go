package harness

import (
	"testing"
	"time"
)

func TestARestartedOutpostWarnsAboutALineThatStillWaits(t *testing.T) {
	svc := NewHTTPService(t, echoHost())
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, photos, "http://"+svc.Addr(), "nas")
	lines := []outpostLine{DeclaredLine("PHOTOS", photos, "http://"+svc.Addr())}
	first := re.ConnectWith(t, "newbox", re.AddOutpost(t, "newbox"), ConnectOptions{Declaration: lines})
	waitWaiting(t, re, "newbox", photos)
	first.Stop()

	olog := new(lockedBuf)
	reconnect(t, re, "newbox", first.StateDir, ConnectOptions{LogOut: olog, Declaration: lines})
	waitFor(t, "the Outpost's warn", 10*time.Second, func() bool {
		return countLines(olog, "level=warn", "variable=TNL_ROUTE_PHOTOS", photos, "held by Outpost nas") == 1
	})
}
