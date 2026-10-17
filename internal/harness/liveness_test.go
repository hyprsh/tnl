package harness

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/state"
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
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), OfflineAfter: testOfflineAfter, LogOut: log, LogLevel: slog.LevelInfo})
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
	waitForEndpointSaved(t, re, "nas")
	re.Stop()

	again := StartEdgeWith(t, EdgeOptions{StateDir: re.StateDir})
	if s := again.Liveness.State("nas"); s != liveness.Online {
		t.Fatalf("state %v right after the restart", s)
	}
	o := lsOutpost(t, again, "nas")
	if o.State != api.StateOnline || o.LastSeen != nil {
		t.Fatalf("outpost ls after the restart: %+v", o)
	}
}

// An Outpost that has handshaken is not shown as never connected after an Edge
// restart, even when the Edge stops before its device poll saved the endpoint.
func TestAnOutpostThatConnectedJustBeforeAnEdgeRestartIsNotNeverConnected(t *testing.T) {
	re := StartEdgeWith(t, EdgeOptions{LivenessPoll: time.Hour, PingInterval: time.Hour})
	o := re.JoinOutpost(t, "nas")
	for _, x := range re.Store.Snapshot().Outposts {
		if x.Name == "nas" && x.Endpoint.IsValid() {
			t.Fatalf("the endpoint was saved before the restart: the test proves nothing")
		}
	}
	// Stop the Outpost so its own ping cannot hide the bug.
	if err := o.Stop(); err != nil {
		t.Fatal(err)
	}
	re.Stop()

	again := StartEdgeWith(t, EdgeOptions{StateDir: re.StateDir, WireGuardPort: re.Port, LivenessPoll: time.Hour})
	if s := again.Liveness.State("nas"); s == liveness.NeverConnected {
		t.Fatalf("state %v right after the restart", s)
	}
	if o := lsOutpost(t, again, "nas"); o.State == api.StateNeverConnected {
		t.Fatalf("outpost ls after the restart: %+v", o)
	}
}

// The endpoint is saved a poll after the first handshake, and is the sign that
// the Outpost has connected before.
func waitForEndpointSaved(t *testing.T, re *RealEdge, name string) {
	t.Helper()
	waitFor(t, "the endpoint to be saved", 5*time.Second, func() bool {
		for _, o := range re.Store.Snapshot().Outposts {
			if o.Name == name {
				return o.Endpoint.IsValid()
			}
		}
		return false
	})
}

func lsOutpost(t *testing.T, re *RealEdge, name string) api.OutpostInfo {
	t.Helper()
	l, err := api.Call[api.OutpostList](ctx(t), re.API, http.MethodGet, api.PathOutposts, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range l.Outposts {
		if o.Name == name {
			return o
		}
	}
	t.Fatalf("no Outpost %s in %+v", name, l)
	return api.OutpostInfo{}
}

// A declared Outpost that has never connected is not online after the Edge
// starts, not counted online by `tnl status`, and Offline once the window
// passes. It is online once it connects.
func TestADeclaredOutpostThatNeverConnectedIsNotOnlineAfterTheEdgeStarts(t *testing.T) {
	d := newDeclaredEdge(t)
	line, _ := d.outpost(t, "nas")
	re := StartEdgeWith(t, d.options(EdgeOptions{OfflineAfter: 3 * time.Second, Outposts: []state.DeclaredOutpost{line}}))

	if o := lsOutpost(t, re, "nas"); o.State != api.StateNeverConnected || o.LastSeen != nil || o.Endpoint != "" {
		t.Fatalf("outpost ls: %+v", o)
	}
	s, text := edgeStatus(t, re)
	if s.Outposts.Online != 0 || s.Outposts.NeverConnected != 1 || len(s.Attention) != 0 {
		t.Fatalf("status: %+v %+v", s.Outposts, s.Attention)
	}
	if !strings.Contains(text, "1 never connected") {
		t.Fatalf("no never connected in\n%s", text)
	}

	waitFor(t, "the Outpost to be Offline", 10*time.Second, func() bool { return lsOutpost(t, re, "nas").State == api.StateOffline })
	s, _ = edgeStatus(t, re)
	if s.Outposts.Offline != 1 || s.Outposts.NeverConnected != 0 || len(s.Attention) != 1 || s.Attention[0].Kind != api.AttentionOutpostOffline {
		t.Fatalf("status past the window: %+v %+v", s.Outposts, s.Attention)
	}
}

// A ping tells the Edge the Outpost's version and a new public endpoint is
// logged at info.
func TestPingsCarryTheVersionAndANewEndpointIsLogged(t *testing.T) {
	log := new(lockedBuf)
	re := StartEdgeWith(t, EdgeOptions{LogOut: log, LogLevel: slog.LevelInfo})
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
