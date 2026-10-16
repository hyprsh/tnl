package liveness

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func newVersionedRig(edgeVersion string) *rig {
	r := &rig{clock: &fake{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, log: new(bytes.Buffer)}
	r.Liveness = New(Config{
		OfflineAfter: 60 * time.Second,
		EdgeVersion:  edgeVersion,
		Now:          r.clock.Now,
		Log:          slog.New(slog.NewTextHandler(r.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	return r
}

func TestAnOutpostInsideTheWindowIsOnline(t *testing.T) {
	r := newVersionedRig("0.3.4")
	r.Heard("home")
	r.SetVersion("home", "0.3.1")
	if got := r.State("home"); got != Online {
		t.Fatalf("state %v", got)
	}
	if strings.Contains(r.log.String(), "level=WARN") {
		t.Fatalf("warned:\n%s", r.log)
	}
}

func TestAnOutpostOutsideTheWindowIsIncompatibleAndWarnedAboutOncePerVersion(t *testing.T) {
	r := newVersionedRig("0.3.4")
	r.Heard("home")
	for i := 0; i < 3; i++ {
		r.SetVersion("home", "0.4.0")
	}
	if got := r.State("home"); got != Incompatible {
		t.Fatalf("state %v", got)
	}
	if n := strings.Count(r.log.String(), "level=WARN"); n != 1 {
		t.Fatalf("%d warnings:\n%s", n, r.log)
	}
	line := r.log.String()
	for _, want := range []string{"outpost=home", "edge_version=0.3.4", "outpost_version=0.4.0", "upgrade the Edge"} {
		if !strings.Contains(line, want) {
			t.Errorf("warning lacks %q:\n%s", want, line)
		}
	}
	// A new version outside the window warns again; one inside it is online.
	r.SetVersion("home", "0.5.0")
	if n := strings.Count(r.log.String(), "level=WARN"); n != 2 {
		t.Fatalf("%d warnings after a second version", n)
	}
	r.SetVersion("home", "0.3.9")
	if got := r.State("home"); got != Online {
		t.Fatalf("state %v after upgrading into the window", got)
	}
	if got := r.Info("home"); got.Version != "0.3.9" {
		t.Fatalf("version %q", got.Version)
	}
}

func TestASilentIncompatibleOutpostIsOffline(t *testing.T) {
	r := newVersionedRig("0.3.4")
	r.Heard("home")
	r.SetVersion("home", "0.4.0")
	r.clock.add(61 * time.Second)
	if got := r.State("home"); got != Offline {
		t.Fatalf("state %v", got)
	}
}

func TestIncompatibleReadsAsIncompatible(t *testing.T) {
	if Incompatible.String() != "incompatible" {
		t.Fatal(Incompatible.String())
	}
}
