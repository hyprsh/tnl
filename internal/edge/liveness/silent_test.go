package liveness

import (
	"testing"
	"time"
)

func newSilentRig() (*rig, *[]string) {
	r := newRig()
	var cut []string
	r.Liveness.cfg.SilentAfter = 20 * time.Second
	r.Liveness.cfg.OnSilent = func(name string) { cut = append(cut, name) }
	return r, &cut
}

func TestAnOutpostSilentForTheBoundHasItsConnectionsClosedButStaysOnline(t *testing.T) {
	r, cut := newSilentRig()
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
	r, cut := newSilentRig()
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

func TestSilencedAtIsWhenTheConnectionsWereLastClosedForSilence(t *testing.T) {
	r, _ := newSilentRig()
	r.Seed("home")
	if !r.SilencedAt("home").IsZero() {
		t.Fatal("silenced before any silence")
	}
	r.clock.add(20 * time.Second)
	r.Check()
	if got := r.SilencedAt("home"); !got.Equal(r.clock.Now()) {
		t.Fatalf("silenced at %v", got)
	}
}

func TestAnOutpostOfflineBeforeTheBoundIsNotClosedForSilence(t *testing.T) {
	r, cut := newSilentRig()
	r.Liveness.cfg.SilentAfter = 2 * time.Minute
	r.Seed("home")
	r.clock.add(2 * time.Minute)
	r.Check()
	if len(*cut) != 0 || len(r.gone) != 1 {
		t.Fatalf("silent closes %v, offline closes %v", *cut, r.gone)
	}
}
