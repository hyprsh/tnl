package liveness

import (
	"bytes"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

type fake struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fake) Now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
func (f *fake) add(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

type rig struct {
	*Liveness
	clock *fake
	log   *bytes.Buffer
	gone  []string
}

func newRig() *rig {
	r := &rig{clock: &fake{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, log: new(bytes.Buffer)}
	r.Liveness = New(Config{
		OfflineAfter: 60 * time.Second,
		Now:          r.clock.Now,
		Log:          slog.New(slog.NewTextHandler(r.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
		OnOffline:    func(name string) { r.gone = append(r.gone, name) },
	})
	return r
}

func TestAnOutpostNobodyHeardOfIsUnknown(t *testing.T) {
	if got := newRig().State("home"); got != Unknown {
		t.Fatalf("state %v", got)
	}
}

func TestSeededOutpostIsOnlineUntilTheWindowPasses(t *testing.T) {
	r := newRig()
	r.Seed("home")
	if r.State("home") != Online {
		t.Fatal("not online right after seeding")
	}
	r.clock.add(59 * time.Second)
	r.Check()
	if r.State("home") != Online || len(r.gone) != 0 {
		t.Fatal("went offline early")
	}
	r.clock.add(time.Second)
	if r.State("home") != Offline {
		t.Fatal("not offline after 60 s of silence, even before a check")
	}
	r.Check()
	r.Check()
	if len(r.gone) != 1 || r.gone[0] != "home" {
		t.Fatalf("OnOffline calls: %v", r.gone)
	}
	if !strings.Contains(r.log.String(), "level=WARN") || !strings.Contains(r.log.String(), "outpost=home") {
		t.Fatalf("no warn line:\n%s", r.log)
	}
}

func TestHeardKeepsItOnline(t *testing.T) {
	r := newRig()
	r.Seed("home")
	for range 5 {
		r.clock.add(50 * time.Second)
		r.Heard("home")
		r.Check()
	}
	if r.State("home") != Online || len(r.gone) != 0 {
		t.Fatal("went offline while being heard")
	}
}

func TestSeedDoesNotRefreshAnOutpostAlreadyTracked(t *testing.T) {
	r := newRig()
	r.Seed("home")
	r.clock.add(40 * time.Second)
	r.Seed("home")
	r.clock.add(20 * time.Second)
	if r.State("home") != Offline {
		t.Fatal("a second Seed counted as hearing")
	}
}

func TestBackOnlineLogsHowLongItWasGone(t *testing.T) {
	r := newRig()
	r.Seed("home")
	r.clock.add(5 * time.Minute)
	r.Check()
	r.Heard("home")
	if r.State("home") != Online {
		t.Fatal("still offline after being heard")
	}
	out := r.log.String()
	if !strings.Contains(out, "level=INFO") || !strings.Contains(out, "gone=5m0s") {
		t.Fatalf("no back-online line with the outage:\n%s", out)
	}
}

func TestHearingAfterTheWindowWithoutACheckStillGoesThroughOffline(t *testing.T) {
	r := newRig()
	r.Seed("home")
	r.clock.add(2 * time.Minute)
	r.Heard("home")
	if len(r.gone) != 1 {
		t.Fatalf("connections were not closed for the outage: %v", r.gone)
	}
	if !strings.Contains(r.log.String(), "gone=2m0s") {
		t.Fatalf("no outage in the log:\n%s", r.log)
	}
}

func TestVersionEndpointAndLastSeen(t *testing.T) {
	r := newRig()
	r.Heard("home")
	r.SetVersion("home", "v1.0.0")
	ep := netip.MustParseAddrPort("203.0.113.5:4000")
	r.SetEndpoint("home", ep)
	r.clock.add(10 * time.Second)
	in := r.Info("home")
	if in.Version != "v1.0.0" || in.Endpoint != ep || in.State != Online || !in.LastSeen.Equal(r.clock.Now().Add(-10*time.Second)) {
		t.Fatalf("info %+v", in)
	}
}

func TestANewEndpointIsLoggedOnceAtInfo(t *testing.T) {
	r := newRig()
	r.Seed("home")
	r.SetEndpoint("home", netip.MustParseAddrPort("203.0.113.5:4000"))
	r.SetEndpoint("home", netip.MustParseAddrPort("203.0.113.5:4000"))
	r.SetEndpoint("home", netip.MustParseAddrPort("203.0.113.9:5000"))
	n := strings.Count(r.log.String(), "level=INFO")
	if n != 2 || !strings.Contains(r.log.String(), "endpoint=203.0.113.9:5000") {
		t.Fatalf("%d info lines:\n%s", n, r.log)
	}
}

func TestASeededOutpostIsNotHeardUntilSomethingArrives(t *testing.T) {
	r := newRig()
	r.Seed("home")
	if i := r.Info("home"); i.Heard {
		t.Fatal("a seeded Outpost counts as heard")
	}
	r.clock.add(5 * time.Second)
	r.Heard("home")
	i := r.Info("home")
	if !i.Heard || i.Silent != 0 {
		t.Fatalf("after hearing: %+v", i)
	}
	r.clock.add(4 * time.Second)
	if i := r.Info("home"); i.Silent != 4*time.Second {
		t.Fatalf("silent for %v, want 4s", i.Silent)
	}
}
