package harness

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/remind"
)

const photos = "photos.test"

func holder(re *RealEdge, host string) string {
	r, ok := re.Store.Snapshot().Route(host)
	if !ok {
		return ""
	}
	return r.Outpost
}

func waitHolder(t *testing.T, re *RealEdge, host, want string) {
	t.Helper()
	waitFor(t, host+" to be held by "+want, 10*time.Second, func() bool { return holder(re, host) == want })
}

func waitWaiting(t *testing.T, re *RealEdge, outpost, host string) {
	t.Helper()
	waitFor(t, outpost+" to wait for "+host, 10*time.Second, func() bool {
		return re.Store.Snapshot().WaitingReason(outpost, host) != "" && len(re.Store.Snapshot().DeclarationOf(outpost)) > 0
	})
}

func TestMovingAServiceBetweenOutpostsWorksWhicheverOutpostChangesFirst(t *testing.T) {
	svc := NewHTTPService(t, echoHost())
	for _, tc := range []struct {
		name      string
		newboxNow bool // newbox declares while nas still holds it
	}{{"declare on newbox, then drop from nas", true}, {"drop from nas, then declare on newbox", false}} {
		t.Run(tc.name, func(t *testing.T) {
			re := StartEdgeWith(t, EdgeOptions{})
			re.AddZone(t, "test")
			line := func(label string) []outpostLine {
				return []outpostLine{DeclaredLine(label, photos, "http://"+svc.Addr())}
			}
			nas := re.ConnectWith(t, "nas", re.AddOutpost(t, "nas"), ConnectOptions{Declaration: line("PHOTOS")})
			waitHolder(t, re, photos, "nas")
			newboxTok := re.AddOutpost(t, "newbox")

			if tc.newboxNow {
				re.ConnectWith(t, "newbox", newboxTok, ConnectOptions{Declaration: line("PHOTOS")})
				waitWaiting(t, re, "newbox", photos)
				if holder(re, photos) != "nas" {
					t.Fatalf("holder %s", holder(re, photos))
				}
				nas.Stop()
				reconnect(t, re, "nas", nas.StateDir, ConnectOptions{})
			} else {
				nas.Stop()
				reconnect(t, re, "nas", nas.StateDir, ConnectOptions{})
				waitFor(t, "the Hostname to be free", 10*time.Second, func() bool { return holder(re, photos) == "" })
				re.ConnectWith(t, "newbox", newboxTok, ConnectOptions{Declaration: line("PHOTOS")})
			}
			waitHolder(t, re, photos, "newbox")
			if re.Store.Snapshot().Declares("nas") {
				t.Fatal("nas still declares")
			}
		})
	}
}

func TestWithTwoOutpostsWaitingForOneHostnameTheFirstToDeclareItGetsIt(t *testing.T) {
	svc := NewHTTPService(t, echoHost())
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	line := []outpostLine{DeclaredLine("PHOTOS", photos, "http://"+svc.Addr())}
	nas := re.ConnectWith(t, "nas", re.AddOutpost(t, "nas"), ConnectOptions{Declaration: line})
	waitHolder(t, re, photos, "nas")
	// "zeta" declares first although "alpha" sorts first.
	re.ConnectWith(t, "zeta", re.AddOutpost(t, "zeta"), ConnectOptions{Declaration: line})
	waitWaiting(t, re, "zeta", photos)
	time.Sleep(5 * time.Millisecond)
	re.ConnectWith(t, "alpha", re.AddOutpost(t, "alpha"), ConnectOptions{Declaration: line})
	waitWaiting(t, re, "alpha", photos)

	nas.Stop()
	reconnect(t, re, "nas", nas.StateDir, ConnectOptions{})
	waitHolder(t, re, photos, "zeta")
	if re.Store.Snapshot().WaitingReason("alpha", photos) == "" {
		t.Fatal("alpha got the Hostname too")
	}
}

func TestRouteRmThatFreesAHostnameSaysWhoServesItNow(t *testing.T) {
	svc := NewHTTPService(t, echoHost())
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, photos, "http://"+svc.Addr(), "nas")
	re.ConnectWith(t, "newbox", re.AddOutpost(t, "newbox"), ConnectOptions{Declaration: []outpostLine{DeclaredLine("PHOTOS", photos, "http://"+svc.Addr())}})
	waitWaiting(t, re, "newbox", photos)

	reply, err := api.Call[api.RouteRmReply](ctx(t), re.API, http.MethodPost, api.PathRouteRm, api.RouteRmRequest{Hostname: photos})
	if err != nil {
		t.Fatal(err)
	}
	if got := reply.Lines(); len(got) != 1 || got[0] != "newbox declared photos.test and now serves it" {
		t.Fatalf("lines %q", got)
	}
	if holder(re, photos) != "newbox" {
		t.Fatalf("holder %q", holder(re, photos))
	}
}

func TestOutpostRmAndADroppedLineHandOverToo(t *testing.T) {
	svc := NewHTTPService(t, echoHost())
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, photos, "http://"+svc.Addr(), "nas")
	re.ConnectWith(t, "newbox", re.AddOutpost(t, "newbox"), ConnectOptions{Declaration: []outpostLine{DeclaredLine("PHOTOS", photos, "http://"+svc.Addr())}})
	waitWaiting(t, re, "newbox", photos)

	reply, err := api.Call[api.OutpostRmReply](ctx(t), re.API, http.MethodPost, api.PathOutpostRm, api.OutpostRmRequest{Name: "nas", WithRoutes: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Served) != 1 || reply.Served[0] != (api.Served{Outpost: "newbox", Hostname: photos}) || holder(re, photos) != "newbox" {
		t.Fatalf("reply %+v, holder %q", reply, holder(re, photos))
	}
}

func TestAZoneAddTurnsAWaitingLineIntoARouteAndZoneRmWithRoutesSendsItBack(t *testing.T) {
	svc := NewHTTPService(t, echoHost())
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	re.ConnectWith(t, "newbox", re.AddOutpost(t, "newbox"), ConnectOptions{Declaration: []outpostLine{
		DeclaredLine("A", "a.test", "http://"+svc.Addr()),
		DeclaredLine("B", "b.other", "http://"+svc.Addr()),
	}})
	waitHolder(t, re, "a.test", "newbox")
	if got := re.Store.Snapshot().WaitingReason("newbox", "b.other"); got != "outside every Zone" {
		t.Fatalf("reason %q", got)
	}

	add, err := api.Call[api.ZoneAddReply](ctx(t), re.API, http.MethodPost, api.PathZones, api.ZoneAddRequest{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if got := add.Lines(); len(got) != 1 || got[0] != "newbox declared b.other and now serves it" {
		t.Fatalf("zone add said %q", got)
	}
	if holder(re, "b.other") != "newbox" {
		t.Fatal("b.other is not a Route")
	}

	rm, err := api.Call[api.ZoneRmReply](ctx(t), re.API, http.MethodPost, api.PathZoneRm, api.ZoneRmRequest{Name: "other", WithRoutes: true})
	if err != nil {
		t.Fatal(err)
	}
	st := re.Store.Snapshot()
	if len(rm.Waiting) != 1 || rm.Waiting[0] != "b.other" || holder(re, "b.other") != "" ||
		st.WaitingReason("newbox", "b.other") != "outside every Zone" || len(st.DeclarationOf("newbox")) != 2 {
		t.Fatalf("zone rm %+v, state %+v", rm, st.Declarations)
	}
}

func TestNoRouteAddTakesAHostnameThatIsBeingFreedForAWaitingLine(t *testing.T) {
	svc := NewHTTPService(t, echoHost())
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	const n = 8
	var lines []outpostLine
	for i := 0; i < n; i++ {
		host := fmt.Sprintf("h%d.test", i)
		re.AddRoute(t, host, "http://"+svc.Addr(), "nas")
		lines = append(lines, DeclaredLine(fmt.Sprintf("H%d", i), host, "http://"+svc.Addr()))
	}
	re.ConnectWith(t, "newbox", re.AddOutpost(t, "newbox"), ConnectOptions{Declaration: lines})
	waitFor(t, "every line to wait", 10*time.Second, func() bool { return len(re.Store.Snapshot().DeclarationOf("newbox")) == n })

	var rivalWon atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		host := fmt.Sprintf("h%d.test", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := api.Call[api.RouteRmReply](ctx(t), re.API, http.MethodPost, api.PathRouteRm, api.RouteRmRequest{Hostname: host}); err != nil {
				t.Error(err)
			}
		}()
		for j := 0; j < 12; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := api.Call[api.RouteAddReply](ctx(t), re.API, http.MethodPost, api.PathRoutes, api.RouteAddRequest{Hostname: host, Service: "http://" + svc.Addr(), Outpost: "nas"})
				if err == nil {
					rivalWon.Add(1)
				}
			}()
		}
	}
	close(start)
	wg.Wait()
	if rivalWon.Load() != 0 {
		t.Fatalf("%d route adds took a Hostname that a waiting line should have got", rivalWon.Load())
	}
	for i := 0; i < n; i++ {
		if h := holder(re, fmt.Sprintf("h%d.test", i)); h != "newbox" {
			t.Errorf("h%d.test is held by %q", i, h)
		}
	}
}

func TestAWaitingLineWarnsOnBothSidesAndRemindsOnAShortenedClock(t *testing.T) {
	svc := NewHTTPService(t, echoHost())
	elog, olog := new(lockedBuf), new(lockedBuf)
	re := StartEdgeWith(t, EdgeOptions{LogOut: elog, LogLevel: slog.LevelInfo, Reminders: remind.Schedule{Every: 300 * time.Millisecond}})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, photos, "http://"+svc.Addr(), "nas")
	re.ConnectWith(t, "newbox", re.AddOutpost(t, "newbox"), ConnectOptions{LogOut: olog, Declaration: []outpostLine{DeclaredLine("PHOTOS", photos, "http://"+svc.Addr())}})

	waitFor(t, "the Edge's warn, reminders and the Outpost's warn", 10*time.Second, func() bool {
		return countLines(elog, "level=warn", "declared line waits", photos, "held by Outpost nas") == 1 &&
			countLines(elog, "level=warn", "declared line still waits", photos) >= 2 &&
			countLines(olog, "level=warn", "variable=TNL_ROUTE_PHOTOS", photos) >= 1
	})

	if _, err := api.Call[api.RouteRmReply](ctx(t), re.API, http.MethodPost, api.PathRouteRm, api.RouteRmRequest{Hostname: photos}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the info lines", 10*time.Second, func() bool {
		return countLines(elog, "level=info", "declared line applies", "outpost=newbox", photos) == 1 &&
			countLines(olog, "level=info", "Route now applies", "variable=TNL_ROUTE_PHOTOS") == 1
	})
	n := countLines(elog, "declared line still waits")
	time.Sleep(time.Second)
	if countLines(elog, "declared line still waits") != n {
		t.Fatal("the Edge kept reminding about a line that applies")
	}
}
