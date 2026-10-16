package edge

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/declare"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/remind"
	"github.com/hyprsh/tnl/internal/state"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

func waitingFixture(t *testing.T) *state.State {
	t.Helper()
	_, pub, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{Version: state.Version}
	st.Zones = []state.Zone{{Name: "example.com", Mode: state.PerHostname}}
	st.Outposts = []state.Outpost{{Name: "nas", PublicKey: pub, Address: st.AllocAddr()}, {Name: "newbox", PublicKey: pub, Address: st.AllocAddr()}}
	st.Routes = []state.Route{{Hostname: "photos.example.com", Outpost: "nas", Service: "http://localhost:1"}}
	st.ApplyDeclaration("newbox", []declare.Line{{Hostname: "photos.example.com", Service: "http://localhost:2"}}, time.Unix(1, 0))
	return st
}

func TestAWaitingLineWarnsOnceThenRemindsOnTheScheduleAndStopsWhenItApplies(t *testing.T) {
	var out bytes.Buffer
	log := slog.New(slog.NewTextHandler(&out, nil))
	clock := &testClock{now: time.Unix(1000, 0)}
	w := newWaitingWatch(log, remind.Schedule{Every: time.Minute}, clock)
	st := waitingFixture(t)

	w.sync(st)
	w.sync(st)
	if n := strings.Count(out.String(), "level=WARN msg=\"declared line waits\""); n != 1 || !strings.Contains(out.String(), "held by Outpost nas") {
		t.Fatalf("log:\n%s", out.String())
	}
	clock.now = clock.now.Add(time.Minute)
	w.remind(st)
	if n := strings.Count(out.String(), "declared line still waits"); n != 1 {
		t.Fatalf("reminders %d:\n%s", n, out.String())
	}

	st.RemoveRoutesVia("nas")
	st.Handover()
	w.sync(st)
	clock.now = clock.now.Add(time.Hour)
	w.remind(st)
	if n := strings.Count(out.String(), "declared line still waits"); n != 1 {
		t.Fatalf("a line that applies kept reminding:\n%s", out.String())
	}
}
