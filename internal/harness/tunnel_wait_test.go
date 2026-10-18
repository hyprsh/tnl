package harness

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

// A request that starts waiting while an Outpost the Edge still counts as
// online is stopped goes through as soon as the Outpost's Tunnel is back, within
// the 10 s wait, instead of failing at the deadline.
func TestARequestWaitingForAReturningOutpostGetsTheServicesReply(t *testing.T) {
	svc := EventService(t)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	if resp, body := Get(t, re.Caddy, H1, "app.test", "/"); resp.StatusCode != 200 || body != "ok" {
		t.Fatalf("before: %d %q", resp.StatusCode, body)
	}
	if err := o.Stop(); err != nil {
		t.Fatal(err)
	}

	type result struct {
		status int
		body   string
		took   time.Duration
	}
	got := make(chan result, 1)
	start := time.Now()
	go func() {
		resp, body := Get(t, re.Caddy, H1, "app.test", "/")
		got <- result{resp.StatusCode, body, time.Since(start)}
	}()
	time.Sleep(3 * time.Second)
	re.Connect(t, "nas", "", o.StateDir)

	select {
	case r := <-got:
		if r.status != 200 || r.body != "ok" {
			t.Fatalf("got %d %q after %v, want the Service's reply", r.status, r.body, r.took)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no answer")
	}
}

// When the Tunnel does not come back, the request fails after 10 s with a reason
// that differs from an Offline Outpost's.
func TestARequestThatWaitsForTheTunnelInVainLogsWhy(t *testing.T) {
	svc := EventService(t)
	log := new(LockedBuf)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), LogOut: log, LogLevel: slog.LevelInfo})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	Get(t, re.Caddy, H1, "app.test", "/") // pays for the certificate
	if err := o.Stop(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, _ := Get(t, re.Caddy, H1, "app.test", "/")
	if resp.StatusCode != 503 || time.Since(start) < 9*time.Second {
		t.Fatalf("got %d after %v, want 503 after the 10 s wait", resp.StatusCode, time.Since(start))
	}
	WaitFor(t, "the request line", 2*time.Second, func() bool {
		l := LineWith(log.String(), "status=503")
		return strings.Contains(l, `reason="tunnel not up"`) && !strings.Contains(l, "outpost offline")
	})
}
