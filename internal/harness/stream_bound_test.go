package harness

import (
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/edge/liveness"
)

// streamBound is the spec's bound: a Visitor's stream through an Outpost whose
// Tunnel connections are gone ends within 15 s (TCP keepalive: 5 s idle, then
// 3 probes 3 s apart), the Offline window being longer. The test allows a
// second or two more for the stream to be noticed by the Visitor's side.
const streamBound = 17 * time.Second

func cutLink(t *testing.T, o *JoinedOutpost, re *RealEdge) {
	t.Helper()
	edgeKey := re.Store.Snapshot().PrivateKey.Public().Hex()
	if err := o.Tunnel.Dev.IpcSet("public_key=" + edgeKey + "\nremove=true\n"); err != nil {
		t.Fatal(err)
	}
}

// When an Outpost's last packets are lost, so its closes never arrive, the
// Edge still ends the Visitor's stream within the bound, long before the
// Offline window (here the default 60 s) is up.
func TestAStreamEndsWithinTheBoundWhenTheOutpostsClosesNeverArrive(t *testing.T) {
	svc := eventService(t)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	get(t, re.Caddy, H1, "app.test", "/")
	next := openStream(t, re, "app.test")
	if !next() {
		t.Fatal("the stream did not start")
	}

	cutLink(t, o, re)
	o.Stop()
	cut := time.Now()
	for next() {
	}
	if d := time.Since(cut); d > streamBound {
		t.Fatalf("the stream ended %v after the Outpost's packets were lost, want within %v", d, streamBound)
	}
	if re.Liveness.State("nas") == liveness.Offline {
		t.Fatal("the Outpost went Offline first: the test shows nothing about keepalive")
	}
}

// An Outpost that restarts leaves older streams through it ended: the new
// process knows nothing of the old connections and resets them.
func TestAnOutpostRestartEndsOlderStreamsThroughIt(t *testing.T) {
	svc := eventService(t)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	get(t, re.Caddy, H1, "app.test", "/")
	next := openStream(t, re, "app.test")
	if !next() {
		t.Fatal("the stream did not start")
	}

	cutLink(t, o, re)
	o.Stop()
	re.Connect(t, "nas", "", o.StateDir)
	restarted := time.Now()
	for next() {
	}
	if d := time.Since(restarted); d > streamBound {
		t.Fatalf("the old stream ended %v after the restart, want within %v", d, streamBound)
	}
	waitFor(t, "the Route to serve again", 10*time.Second, func() bool {
		resp, _ := get(t, re.Caddy, H1, "app.test", "/")
		return resp.StatusCode == 200
	})
}
