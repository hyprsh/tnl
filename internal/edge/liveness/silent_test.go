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

func TestAConnectionOpenedDuringASilenceIsClosedOnceAndLogged(t *testing.T) {
	r, cut, open := newSilentRig()
	*open = false
	r.Seed("home")
	r.clock.add(20 * time.Second)
	r.Check()
	*open = true
	r.clock.add(time.Second)
	r.Check()
	r.Check()
	if strings.Count(r.log.String(), "outpost silent") != 1 {
		t.Fatalf("want one info line:\n%s", r.log)
	}
	n := len(*cut)
	r.clock.add(time.Second)
	r.Check()
	if len(*cut) != n {
		t.Fatal("closed again in the same silence")
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
