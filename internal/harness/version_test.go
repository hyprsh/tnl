package harness

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/edge/liveness"
)

// reconnectAs stops the Outpost and starts it again from its state as another
// version, as an operator who upgrades the Outpost first would.
func reconnectAs(t *testing.T, re *RealEdge, name string, j *JoinedOutpost, version string) *JoinedOutpost {
	t.Helper()
	if err := j.Stop(); err != nil {
		t.Fatal(err)
	}
	again, err := re.TryConnectWith(t, name, "", j.StateDir, ConnectOptions{Version: version})
	if err != nil {
		t.Fatal(err)
	}
	return again
}

// An Outpost 0.3.1 on an Edge 0.3.4 serves; after it is upgraded to 0.4.0 it is
// Incompatible: its Routes answer 503, its API calls are refused with the
// spec's message, and the Edge warns once.
func TestAnOutpostOutsideTheVersionWindowIsIncompatible(t *testing.T) {
	svc := EventService(t)
	log := new(LockedBuf)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), Version: "0.3.4", LogOut: log, LogLevel: slog.LevelInfo})
	re.AddZone(t, "test")
	tok := re.AddOutpost(t, "nas")
	j, err := re.TryConnectWith(t, "nas", tok, mustTempDir(t), ConnectOptions{JoinTimeout: 3 * time.Second, Version: "0.3.1"})
	if err != nil {
		t.Fatal(err)
	}
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	WaitFor(t, "the Outpost's version", 5*time.Second, func() bool { return re.Liveness.Info("nas").Version == "0.3.1" })
	if resp, body := Get(t, re.Caddy, H1, "app.test", "/"); resp.StatusCode != 200 || body != "ok" {
		t.Fatalf("inside the window: %d %q", resp.StatusCode, body)
	}
	if _, err := api.Call[api.ZoneList](context.Background(), j.API, http.MethodGet, api.PathZones, nil); err != nil {
		t.Fatalf("inside the window, an API call: %v", err)
	}

	j = reconnectAs(t, re, "nas", j, "0.4.0")
	WaitFor(t, "the Outpost to be Incompatible", 5*time.Second, func() bool { return re.Liveness.State("nas") == liveness.Incompatible })

	resp, _ := Get(t, re.Caddy, H1, "app.test", "/")
	if resp.StatusCode != 503 {
		t.Fatalf("incompatible: %d, want 503", resp.StatusCode)
	}
	_, err = api.Call[api.ZoneList](context.Background(), j.API, http.MethodGet, api.PathZones, nil)
	var ae *api.Error
	want := "the Edge (0.3.4) doesn't accept this Outpost (0.4.0); upgrade the Edge"
	if !errors.As(err, &ae) || ae.Message != want {
		t.Fatalf("API call: %v, want %q", err, want)
	}
	// The Edge's pong tells the Outpost, which says so once.
	time.Sleep(500 * time.Millisecond)
	if n := strings.Count(log.String(), "outpost is incompatible"); n != 1 {
		t.Fatalf("%d warnings, want 1:\n%s", n, log)
	}
	line := LineWith(log.String(), "outpost is incompatible")
	for _, w := range []string{"level=warn", "outpost=nas", "edge_version=0.3.4", "outpost_version=0.4.0"} {
		if !strings.Contains(line, w) {
			t.Errorf("warning lacks %q: %s", w, line)
		}
	}

	// Upgrading the Edge to 0.4.x brings it back.
	j.Stop()
	re.Stop()
	re2 := StartEdgeWith(t, EdgeOptions{StateDir: re.StateDir, Pebble: StartPebble(t, PebbleOptions{}), Version: "0.4.1", WireGuardPort: re.Port})
	if _, err := re2.TryConnectWith(t, "nas", "", j.StateDir, ConnectOptions{Version: "0.4.0"}); err != nil {
		t.Fatal(err)
	}
	WaitFor(t, "the Outpost to be online again", 5*time.Second, func() bool {
		return re2.Liveness.State("nas") == liveness.Online && re2.Liveness.Info("nas").Version == "0.4.0"
	})
}

// An Outpost refuses an Edge of an older major in a join token, naming both
// versions. Met in a ping it never exits: it logs the error once per Edge
// version, keeps pinging and recovers when the Edge is rolled forward.
func TestAnOutpostRefusesAnEdgeOfAnOlderMajor(t *testing.T) {
	re := StartEdgeWith(t, EdgeOptions{Version: "1.4.2"})
	tok := re.AddOutpost(t, "nas")
	_, err := re.TryConnectWith(t, "nas", tok, mustTempDir(t), ConnectOptions{Version: "2.0.0"})
	if err == nil || !strings.Contains(err.Error(), "1.4.2") || !strings.Contains(err.Error(), "2.0.0") {
		t.Fatalf("join: %v", err)
	}

	// Joined to an Edge 2.0.0 and met again as 1.4.2 after a rollback.
	dir := mustTempDir(t)
	re2 := StartEdgeWith(t, EdgeOptions{Version: "2.0.0"})
	j, err := re2.TryConnectWith(t, "nas", re2.AddOutpost(t, "nas"), dir, ConnectOptions{Version: "2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	j.Stop()
	re2.Stop()
	back := StartEdgeWith(t, EdgeOptions{StateDir: re2.StateDir, Version: "1.4.2", WireGuardPort: re2.Port})
	log := new(LockedBuf)
	again, err := back.TryConnectWith(t, "nas", "", dir, ConnectOptions{Version: "2.0.0", LogOut: log})
	if err != nil {
		t.Fatal(err)
	}
	WaitFor(t, "the error", 10*time.Second, func() bool { return strings.Contains(log.String(), "this Edge is too old for this Outpost") })
	if line := LineWith(log.String(), "this Edge is too old for this Outpost"); !strings.Contains(line, "level=error") || !strings.Contains(line, "1.4.2") || !strings.Contains(line, "2.0.0") {
		t.Fatalf("error line: %q", line)
	}
	// It does not exit, and says it once.
	time.Sleep(time.Second)
	if n := strings.Count(log.String(), "this Edge is too old for this Outpost"); n != 1 {
		t.Fatalf("%d errors, want 1:\n%s", n, log)
	}

	// `tnl status` needs attention for it, as its own fact, not as Incompatible.
	if s, _ := outpostStatus(t, again); !s.Edge.TooOld || s.Edge.Incompatible || len(s.Attention) != 1 || s.Attention[0].Kind != api.AttentionEdgeTooOld {
		t.Errorf("status of an Outpost with an Edge too old: %+v", s)
	}

	// Rolled forward, the same Outpost process is Online again.
	back.Stop()
	fwd := StartEdgeWith(t, EdgeOptions{StateDir: re2.StateDir, Version: "2.0.0", WireGuardPort: re2.Port})
	WaitFor(t, "the Outpost to be online again", 15*time.Second, func() bool {
		return fwd.Liveness.State("nas") == liveness.Online && fwd.Liveness.Info("nas").Version == "2.0.0"
	})
	if err := again.Stop(); err != nil {
		t.Fatalf("the Outpost exited with %v", err)
	}
}

// A request field the Edge doesn't know is refused whole, naming the Edge's
// version and the side to upgrade.
func TestAnUnknownRequestFieldIsRefusedNamingTheEdgesVersion(t *testing.T) {
	re := StartEdgeWith(t, EdgeOptions{Version: "1.4.2"})
	_, err := api.Call[api.ZoneAddReply](context.Background(), re.API, http.MethodPost, api.PathZones, map[string]any{"name": "example.com", "new_opt": true})
	var ae *api.Error
	want := "the Edge (1.4.2) doesn't support `new_opt`; it needs a newer version; upgrade the Edge"
	if !errors.As(err, &ae) || ae.Status != 400 || ae.Message != want {
		t.Fatalf("err = %v", err)
	}
	if n := len(re.Store.Snapshot().Zones); n != 0 {
		t.Fatalf("%d Zones: the request was not refused whole", n)
	}
}
