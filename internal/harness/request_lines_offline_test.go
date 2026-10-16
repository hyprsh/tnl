package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/edge/liveness"
)

// A request to an Offline Outpost is answered 503 with a request line that
// names the reason, and a request cut because the Outpost's connection dropped
// gets a line of its own.
func TestRequestLinesForAnOfflineOutpostAndForACutConnection(t *testing.T) {
	svc := eventService(t)
	log := new(lockedBuf)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), OfflineAfter: testOfflineAfter, LogOut: log})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	next := openStream(t, re, "app.test")
	if !next() {
		t.Fatal("the stream did not start")
	}

	// Cut the link, so the Edge ends the stream when the Outpost goes Offline.
	edgeKey := re.Store.Snapshot().PrivateKey.Public().Hex()
	if err := o.Tunnel.Dev.IpcSet("public_key=" + edgeKey + "\nremove=true\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the stream to be closed", 5*testOfflineAfter+time.Second, func() bool { return !next() })
	waitFor(t, "the Outpost to be Offline", 5*testOfflineAfter, func() bool { return re.Liveness.State("nas") == liveness.Offline })

	waitFor(t, "the request line of the cut stream", 2*time.Second, func() bool {
		for _, l := range strings.Split(log.String(), "\n") {
			if strings.Contains(l, "msg=request") && strings.Contains(l, "path=/sse") {
				return strings.Contains(l, "level=info") && strings.Contains(l, `reason="outpost offline"`) && strings.Contains(l, "outpost=nas")
			}
		}
		return false
	})

	if resp, _ := get(t, re.Caddy, H1, "app.test", "/"); resp.StatusCode != 503 {
		t.Fatalf("offline: %d, want 503", resp.StatusCode)
	}
	var line string
	for _, l := range strings.Split(log.String(), "\n") {
		if strings.Contains(l, "msg=request") && strings.Contains(l, "status=503") {
			line = l
		}
	}
	for _, want := range []string{"level=info", "host=app.test", "status=503", `reason="outpost offline"`, "outpost=nas"} {
		if !strings.Contains(line, want) {
			t.Errorf("the 503's request line lacks %q:\n%s", want, log)
		}
	}
}
