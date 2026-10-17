package liveness

import (
	"strings"
	"testing"
	"time"
)

// newSilentRig has an OnSilent that closes the Outpost's connections when open
// is true, and records every call it is made.
func newSilentRig() (r *rig, calls *[]string, open *bool) {
	r = newRig()
	calls, open = new([]string), new(bool)
	*open = true
	r.Liveness.cfg.SilentAfter = 20 * time.Second
	r.Liveness.cfg.OnSilent = func(name string) bool {
		*calls = append(*calls, name)
		return *open
	}
	return r, calls, open
}

func TestAnOutpostSilentForTheBoundHasItsConnectionsClosedButStaysOnline(t *testing.T) {
	r, cut, _ := newSilentRig()
	r.Seed("home")
	r.clock.add(19 * time.Second)
	r.Check()
	if len(*cut) != 0 {
		t.Fatal("closed early")
	}
	r.clock.add(time.Second)
	r.Check()
	if len(*cut) != 1 || (*cut)[0] != "home" {
		t.Fatalf("closed %v", *cut)
	}
	if !strings.Contains(r.log.String(), "outpost silent, closing its connections") {
		t.Fatalf("no info line:\n%s", r.log)
	}
	if r.State("home") != Online || len(r.gone) != 0 {
		t.Fatal("the Outpost's state changed")
	}
	r.clock.add(5 * time.Second)
	r.Check()
	if len(*cut) != 1 {
		t.Fatal("closed again in the same silence")
	}
}

func TestHearingFromAnOutpostRestartsTheSilence(t *testing.T) {
	r, cut, _ := newSilentRig()
	r.Seed("home")
	r.clock.add(19 * time.Second)
	r.Heard("home")
	r.clock.add(19 * time.Second)
	r.Check()
	if len(*cut) != 0 {
		t.Fatal("closed after being heard")
	}
	r.clock.add(time.Second)
	r.Check()
	r.Heard("home")
	r.clock.add(20 * time.Second)
	r.Check()
	if len(*cut) != 2 {
		t.Fatalf("a second silence closed %d times in all, want 2", len(*cut))
	}
}

func TestASilenceWithNoConnectionsToCloseIsNeitherLoggedNorCounted(t *testing.T) {
	r, _, open := newSilentRig()
	*open = false
	r.Seed("home")
	r.clock.add(20 * time.Second)
	r.Check()
	if strings.Contains(r.log.String(), "silent") {
		t.Fatalf("logged a close that closed nothing:\n%s", r.log)
	}
}

func TestASilenceCloseIsAskedForOncePerSilenceEvenWhenItClosedNothing(t *testing.T) {
	r, asked, open := newSilentRig()
	*open = false
	r.Seed("home")
	r.clock.add(20 * time.Second)
	r.Check()
	*open = true // a connection opens later in the same silence
	r.clock.add(time.Second)
	r.Check()
	r.Check()
	if len(*asked) != 1 {
		t.Fatalf("asked %d times in one silence, want 1", len(*asked))
	}
	if strings.Contains(r.log.String(), "outpost silent") {
		t.Fatalf("logged a close that closed nothing:\n%s", r.log)
	}
}

func TestHearingFromAnOutpostRearmsTheSilenceCloseAfterAnEmptyAsk(t *testing.T) {
	r, asked, open := newSilentRig()
	*open = false
	r.Seed("home")
	r.clock.add(20 * time.Second)
	r.Check()
	r.Heard("home")
	*open = true
	r.clock.add(20 * time.Second)
	r.Check()
	if len(*asked) != 2 {
		t.Fatalf("asked %d times over two silences, want 2", len(*asked))
	}
	if strings.Count(r.log.String(), "outpost silent") != 1 {
		t.Fatalf("want one info line:\n%s", r.log)
	}
}

func TestAnOutpostHeardAfterTheSilentListWasBuiltIsNotAsked(t *testing.T) {
	r, asked, _ := newSilentRig()
	r.Seed("other")
	r.clock.add(40 * time.Second)
	r.Seed("home")
	r.clock.add(20 * time.Second) // other is Offline, home has been silent for 20 s
	// Offline hooks run after the list is built and before the closes.
	r.Liveness.cfg.OnOffline = func(string) { r.Heard("home") }
	r.Check()
	if len(*asked) != 0 {
		t.Fatalf("asked to close %v for an Outpost heard just before", *asked)
	}
}

func TestAnOutpostOfflineBeforeTheBoundIsNotClosedForSilence(t *testing.T) {
	r, cut, _ := newSilentRig()
	r.Liveness.cfg.SilentAfter = 2 * time.Minute
	r.Seed("home")
	r.clock.add(2 * time.Minute)
	r.Check()
	if len(*cut) != 0 || len(r.gone) != 1 {
		t.Fatalf("silent closes %v, offline closes %v", *cut, r.gone)
	}
}
