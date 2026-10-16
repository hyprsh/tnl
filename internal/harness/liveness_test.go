package harness

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/edge/liveness"
)

const testOfflineAfter = 600 * time.Millisecond

func waitFor(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func eventService(t *testing.T) *Service {
	return NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sse" {
			fmt.Fprint(w, "ok")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		rc := http.NewResponseController(w)
		for {
			if _, err := fmt.Fprint(w, "data: x\n\n"); err != nil || rc.Flush() != nil {
				return
			}
			select {
			case <-time.After(20 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
	}))
}

// Stopping an Outpost turns its Routes to 503 within the Offline window and
// closes its open streams; when it comes back its Routes serve again and the
// Edge logs the outage.
func TestStoppingAnOutpostTurnsItsRoutesTo503AndItComesBack(t *testing.T) {
	svc := eventService(t)
	log := new(lockedBuf)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), OfflineAfter: testOfflineAfter, LogOut: log})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	if resp, body := get(t, re.Caddy, H1, "app.test", "/"); resp.StatusCode != 200 || body != "ok" {
		t.Fatalf("before: %d %q", resp.StatusCode, body)
	}
	next := openStream(t, re, "app.test")
	if !next() {
		t.Fatal("the stream did not start")
	}

	if err := o.Stop(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the stream to be closed", 5*testOfflineAfter+time.Second, func() bool { return !next() })
	waitFor(t, "the Outpost to be Offline", 5*testOfflineAfter, func() bool { return re.Liveness.State("nas") == liveness.Offline })
	start := time.Now()
	resp, _ := get(t, re.Caddy, H1, "app.test", "/")
	if resp.StatusCode != 503 || time.Since(start) > 2*time.Second {
		t.Fatalf("offline: %d after %v, want 503 at once", resp.StatusCode, time.Since(start))
	}
	waitFor(t, "the Offline line", 2*time.Second, func() bool {
		return strings.Contains(log.String(), "level=warn msg=\"outpost offline\" outpost=nas")
	})

	re.Connect(t, "nas", "", o.StateDir)
	waitFor(t, "the Route to serve again", 10*time.Second, func() bool {
		resp, _ := get(t, re.Caddy, H1, "app.test", "/")
		return resp.StatusCode == 200
	})
	if line := lineWith(log.String(), "back online"); !strings.Contains(line, "level=info") || !strings.Contains(line, "outpost=nas") || !strings.Contains(line, "gone=") {
		t.Fatalf("no back-online line with the outage in:\n%s", log)
	}
}

// An Outpost that goes silent without closing anything (a dead link) has its
// open stream closed by the Edge once the Offline window is up, not before.
func TestAnOutpostThatGoesSilentHasItsStreamsClosedWhenItGoesOffline(t *testing.T) {
	svc := eventService(t)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), OfflineAfter: testOfflineAfter})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	get(t, re.Caddy, H1, "app.test", "/") // pays for the certificate
	next := openStream(t, re, "app.test")
	if !next() {
		t.Fatal("the stream did not start")
	}

	// Cut the link: the Outpost forgets the Edge's peer, so nothing crosses.
	edgeKey := re.Store.Snapshot().PrivateKey.Public().Hex()
	if err := o.Tunnel.Dev.IpcSet("public_key=" + edgeKey + "\nremove=true\n"); err != nil {
		t.Fatal(err)
	}
	cut := time.Now()
	for next() {
	}
	if d := time.Since(cut); d < testOfflineAfter/2 || d > 5*time.Second {
		t.Fatalf("the stream ended %v after the link was cut, want about the Offline window", d)
	}
	if re.Liveness.State("nas") != liveness.Offline {
		t.Fatal("the Outpost is not Offline")
	}
}

func lineWith(log, sub string) string {
	for _, l := range strings.Split(log, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

// The Edge's start counts as hearing from every Outpost, before any ping.
func TestAfterAnEdgeRestartOutpostsCountAsOnlineBeforeTheirFirstPing(t *testing.T) {
	re := StartEdge(t)
	re.JoinOutpost(t, "nas")
	re.Stop()

	again := StartEdgeWith(t, EdgeOptions{StateDir: re.StateDir})
	if s := again.Liveness.State("nas"); s != liveness.Online {
		t.Fatalf("state %v right after the restart", s)
	}
}

// A ping tells the Edge the Outpost's version and a new public endpoint is
// logged at info.
func TestPingsCarryTheVersionAndANewEndpointIsLogged(t *testing.T) {
	log := new(lockedBuf)
	re := StartEdgeWith(t, EdgeOptions{LogOut: log})
	re.JoinOutpost(t, "nas")
	waitFor(t, "the Outpost's version", 5*time.Second, func() bool { return re.Liveness.Info("nas").Version == harnessVersion })
	waitFor(t, "the endpoint line", 5*time.Second, func() bool { return strings.Contains(log.String(), "new public endpoint") })
	if line := lineWith(log.String(), "new public endpoint"); !strings.Contains(line, "level=info") || !strings.Contains(line, "outpost=nas") || !strings.Contains(line, "endpoint=127.0.0.1:") {
		t.Fatalf("endpoint line: %s", line)
	}
	if re.Liveness.Info("nas").Endpoint.Addr().String() != "127.0.0.1" {
		t.Fatalf("endpoint %v", re.Liveness.Info("nas").Endpoint)
	}
}

// `route add` on an Offline Outpost saves the Route and warns.
func TestRouteAddOnAnOfflineOutpostWarns(t *testing.T) {
	re := StartEdgeWith(t, EdgeOptions{OfflineAfter: testOfflineAfter})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	o.Stop()
	waitFor(t, "the Outpost to go Offline", 5*time.Second, func() bool { return re.Liveness.State("nas") == liveness.Offline })
	r, err := api.Call[api.RouteAddReply](context.Background(), re.API, http.MethodPost, api.PathRoutes,
		api.RouteAddRequest{Hostname: "app.test", Service: "http://localhost:3000", Outpost: "nas"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings.Warnings) != 1 || !strings.Contains(r.Warnings.Warnings[0], "Offline") {
		t.Fatalf("warnings %q", r.Warnings.Warnings)
	}
}
