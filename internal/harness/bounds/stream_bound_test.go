package bounds

import (
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/outpost"

	. "github.com/hyprsh/tnl/internal/harness"
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
	svc := EventService(t)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	Get(t, re.Caddy, H1, "app.test", "/")
	next := OpenStream(t, re, "app.test")
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

// restartBound is how long the Edge takes to close the older streams through a
// restarted Outpost: its first ping with a new session id, 100 ms apart in the
// harness, after the handshake. Keepalive would take 5 s at the least.
const restartBound = 3 * time.Second

// An Outpost that restarts leaves older streams through it ended at once: its
// first ping carries a new session id, and the Edge closes the connections
// through it, without waiting for TCP keepalive.
func TestAnOutpostRestartEndsOlderStreamsThroughIt(t *testing.T) {
	svc := EventService(t)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	Get(t, re.Caddy, H1, "app.test", "/")
	next := OpenStream(t, re, "app.test")
	if !next() {
		t.Fatal("the stream did not start")
	}

	cutLink(t, o, re)
	o.Stop()
	re.Connect(t, "nas", "", o.StateDir)
	restarted := time.Now()
	for next() {
	}
	if d := time.Since(restarted); d > restartBound {
		t.Fatalf("the old stream ended %v after the restart, want within %v", d, restartBound)
	}
	WaitFor(t, "the Route to serve again", 10*time.Second, func() bool {
		resp, _ := Get(t, re.Caddy, H1, "app.test", "/")
		return resp.StatusCode == 200
	})
}

// A stopped Outpost lets its closes leave through WireGuard before it tears
// the Tunnel down, so a Visitor's stream ends at once, not after keepalive.
func TestStoppingAnOutpostEndsAVisitorsStreamAtOnce(t *testing.T) {
	svc := EventService(t)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	Get(t, re.Caddy, H1, "app.test", "/")
	next := OpenStream(t, re, "app.test")
	if !next() {
		t.Fatal("the stream did not start")
	}

	stopped := time.Now()
	o.Stop()
	for next() {
	}
	if d := time.Since(stopped); d > 2*time.Second {
		t.Fatalf("the stream ended %v after the Outpost stopped, want within 2s (keepalive would take 15s)", d)
	}
}

// With the Edge unreachable the closes cannot leave, and the Outpost still
// exits within the drain cap.
func TestStoppingAnOutpostWhoseEdgeIsUnreachableStillExitsWithinTheCap(t *testing.T) {
	svc := EventService(t)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	Get(t, re.Caddy, H1, "app.test", "/")
	next := OpenStream(t, re, "app.test")
	if !next() {
		t.Fatal("the stream did not start")
	}

	cutLink(t, o, re)
	start := time.Now()
	o.Stop()
	if d := time.Since(start); d > outpost.DrainCap+time.Second {
		t.Fatalf("Stop took %v with the Edge unreachable, want within the %v cap plus a second", d, outpost.DrainCap)
	}
}
