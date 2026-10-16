package remind

import (
	"slices"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newTracker(s Schedule) (*Tracker, *fakeClock) {
	c := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	return NewTracker(s, c), c
}

// due advances the clock by d and returns the keys due.
func due(tr *Tracker, c *fakeClock, d time.Duration) []string {
	c.advance(d)
	var keys []string
	for _, r := range tr.Due() {
		keys = append(keys, r.Key)
	}
	return keys
}

func TestBeginReportsOnlyTheFirstTime(t *testing.T) {
	tr, _ := newTracker(Standard)
	if !tr.Begin("x") {
		t.Fatal("first Begin should report a start")
	}
	if tr.Begin("x") {
		t.Fatal("second Begin should not report a start")
	}
}

func TestStandardRemindsAt5Min30MinThenHourly(t *testing.T) {
	tr, c := newTracker(Standard)
	tr.Begin("x")
	// Offsets from the start: 5m, 30m, 1h30m, 2h30m.
	steps := []struct {
		advance time.Duration
		want    bool
	}{
		{4 * time.Minute, false},
		{time.Minute, true}, // 5m
		{24 * time.Minute, false},
		{time.Minute, true}, // 30m
		{59 * time.Minute, false},
		{time.Minute, true}, // 1h30m
		{time.Hour, true},   // 2h30m
	}
	for i, s := range steps {
		got := due(tr, c, s.advance)
		if (len(got) == 1) != s.want {
			t.Fatalf("step %d: due=%v, want due=%v", i, got, s.want)
		}
	}
}

func TestDailyRemindsEveryDay(t *testing.T) {
	tr, c := newTracker(Daily)
	tr.Begin("x")
	if got := due(tr, c, 23*time.Hour); got != nil {
		t.Fatalf("due after 23h: %v", got)
	}
	if got := due(tr, c, time.Hour); !slices.Equal(got, []string{"x"}) {
		t.Fatalf("due after 24h: %v", got)
	}
	if got := due(tr, c, 24*time.Hour); !slices.Equal(got, []string{"x"}) {
		t.Fatalf("due after 48h: %v", got)
	}
}

func TestDueReportsHowLongItHasBeenGoing(t *testing.T) {
	tr, c := newTracker(Standard)
	tr.Begin("x")
	c.advance(5 * time.Minute)
	got := tr.Due()
	if len(got) != 1 || got[0].Since != 5*time.Minute {
		t.Fatalf("got %+v", got)
	}
}

func TestEndStopsReminders(t *testing.T) {
	tr, c := newTracker(Standard)
	tr.Begin("x")
	c.advance(10 * time.Minute)
	since, was := tr.End("x")
	if !was || since != 10*time.Minute {
		t.Fatalf("End = %v, %v", since, was)
	}
	if got := due(tr, c, time.Hour); got != nil {
		t.Fatalf("due after End: %v", got)
	}
	if _, was := tr.End("x"); was {
		t.Fatal("End of something not going reported it was")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	tr, c := newTracker(Standard)
	tr.Begin("a")
	c.advance(3 * time.Minute)
	tr.Begin("b")
	if got := due(tr, c, 2*time.Minute); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("due: %v", got)
	}
}
