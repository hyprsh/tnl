package logx

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestRingKeepsTheLastThousandLines(t *testing.T) {
	var out bytes.Buffer
	lg := New(Options{Level: slog.LevelInfo, Out: &out})
	for i := 0; i < 1005; i++ {
		lg.Info("line", "i", i)
	}
	got := RingOf(lg).Snapshot()
	if len(got) != 1000 {
		t.Fatalf("ring holds %d lines, want 1000", len(got))
	}
	if !strings.Contains(got[0].Text, "i=5") || !strings.Contains(got[999].Text, "i=1004") {
		t.Errorf("want lines 5..1004, got %q ... %q", got[0].Text, got[999].Text)
	}
}

func TestRingIsBoundedByBytes(t *testing.T) {
	lg := New(Options{Level: slog.LevelInfo, Out: &bytes.Buffer{}})
	big := strings.Repeat("x", 100<<10)
	for i := 0; i < 30; i++ {
		lg.Info("big", "i", i, "pad", big)
	}
	got := RingOf(lg).Snapshot()
	size := 0
	for _, e := range got {
		size += len(e.Text)
	}
	if size > 1<<20 || len(got) < 5 {
		t.Errorf("ring holds %d lines, %d bytes; want under 1 MiB and the newest lines", len(got), size)
	}
	if !strings.Contains(got[len(got)-1].Text, "i=29") {
		t.Errorf("newest line missing: %.80q", got[len(got)-1].Text)
	}
}

func TestRingHoldsOnlyWhatIsLoggedAndKeepsTheTimeInJournalMode(t *testing.T) {
	lg := New(Options{Level: slog.LevelWarn, Journal: true, Out: &bytes.Buffer{}})
	lg.Info("quiet")
	lg.Warn("loud", "k", "v")
	got := RingOf(lg).Snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1", len(got))
	}
	e := got[0]
	if !strings.HasPrefix(e.Text, "time=") || !strings.Contains(e.Text, "level=warn msg=loud k=v") || strings.HasPrefix(e.Text, "<") {
		t.Errorf("text line = %q", e.Text)
	}
	if !strings.Contains(e.JSON, `"msg":"loud"`) || e.Level != slog.LevelWarn || time.Since(e.Time) > time.Minute {
		t.Errorf("entry = %+v", e)
	}
}

func TestRingKeepsAttrsOfDerivedLoggers(t *testing.T) {
	lg := New(Options{Level: slog.LevelInfo, JSON: true, Out: &bytes.Buffer{}})
	lg.With("component", "x").WithGroup("g").Info("hi", "a", 1)
	got := RingOf(lg).Snapshot()
	if len(got) != 1 || !strings.Contains(got[0].Line(true), `"component":"x"`) || !strings.Contains(got[0].Line(true), `"g":{"a":1}`) {
		t.Fatalf("got %+v", got)
	}
	if !RingOf(lg).JSON() {
		t.Error("ring should report the process logs JSON")
	}
}

func TestAccessLinesStayOutOfTheRing(t *testing.T) {
	var out bytes.Buffer
	lg := New(Options{Level: slog.LevelInfo, Out: &out})
	lg.InfoContext(AccessContext(context.Background()), "request", "status", 200)
	lg.Info("own line")
	if !strings.Contains(out.String(), "msg=request") {
		t.Error("access line missing from stderr")
	}
	got := RingOf(lg).Snapshot()
	if len(got) != 1 || !strings.Contains(got[0].Text, "own line") {
		t.Errorf("ring = %+v", got)
	}
}

func TestFollowerSeesNewLinesAndASlowOneNeverBlocksLogging(t *testing.T) {
	lg := New(Options{Level: slog.LevelInfo, Out: &bytes.Buffer{}})
	r := RingOf(lg)
	lg.Info("before")
	snap, ch, stop := r.Follow()
	defer stop()
	if len(snap) != 1 {
		t.Fatalf("snapshot = %d lines", len(snap))
	}
	lg.Info("after")
	select {
	case e := <-ch:
		if !strings.Contains(e.Text, "after") {
			t.Errorf("got %q", e.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("follower saw nothing")
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ { // nobody reads ch
			lg.Info(fmt.Sprint("n", i))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("logging blocked on a slow follower")
	}
}
