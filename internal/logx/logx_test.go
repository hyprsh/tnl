package logx

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"

	"golang.zx2c4.com/wireguard/device"
)

func TestTextLinesAreKeyValueWithLowercaseLevel(t *testing.T) {
	var buf bytes.Buffer
	lg := New(Options{Level: slog.LevelInfo, Out: &buf})
	lg.Warn("disk low", "dir", "/x")
	line := buf.String()
	for _, want := range []string{"level=warn", `msg="disk low"`, "dir=/x", "time="} {
		if !strings.Contains(line, want) {
			t.Errorf("%q lacks %q", line, want)
		}
	}
}

func TestJSONHasTheSameFields(t *testing.T) {
	var buf bytes.Buffer
	lg := New(Options{Level: slog.LevelInfo, JSON: true, Out: &buf})
	lg.Error("boom", "dir", "/x")
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["level"] != "error" || m["msg"] != "boom" || m["dir"] != "/x" || m["time"] == nil {
		t.Fatalf("got %v", m)
	}
}

func TestLevelsFilter(t *testing.T) {
	var buf bytes.Buffer
	lg := New(Options{Level: slog.LevelWarn, Out: &buf})
	lg.Info("quiet")
	lg.Debug("quieter")
	if buf.Len() != 0 {
		t.Fatalf("got %q", buf.String())
	}
	lg.Error("loud")
	if buf.Len() == 0 {
		t.Fatal("error was filtered")
	}
}

func TestJournalDropsTimeAndPrefixesPriority(t *testing.T) {
	for _, tc := range []struct {
		json   bool
		prefix string
		level  slog.Level
	}{
		{false, "<3>", slog.LevelError},
		{false, "<4>", slog.LevelWarn},
		{false, "<6>", slog.LevelInfo},
		{true, "<7>", slog.LevelDebug},
	} {
		var buf bytes.Buffer
		lg := New(Options{Level: slog.LevelDebug, JSON: tc.json, Journal: true, Out: &buf})
		lg.Log(context.Background(), tc.level, "hi", "k", "v")
		line := buf.String()
		if !strings.HasPrefix(line, tc.prefix) {
			t.Errorf("json=%v: %q lacks prefix %s", tc.json, line, tc.prefix)
		}
		if strings.Contains(line, "time") {
			t.Errorf("json=%v: %q has a timestamp", tc.json, line)
		}
		if strings.Count(line, "\n") != 1 {
			t.Errorf("want one line, got %q", line)
		}
	}
}

func TestJournalLoggerKeepsWithAttrsAndGroups(t *testing.T) {
	var buf bytes.Buffer
	lg := New(Options{Level: slog.LevelInfo, Journal: true, Out: &buf})
	lg.With("actor", "local").WithGroup("g").Info("hi", "k", "v")
	line := buf.String()
	if !strings.Contains(line, "actor=local") || !strings.Contains(line, "g.k=v") {
		t.Fatalf("got %q", line)
	}
}

func TestOnJournal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "j")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dev, ino := statOf(t, f)
	good := strconv.FormatUint(dev, 10) + ":" + strconv.FormatUint(ino, 10)
	if !OnJournal(f, good) {
		t.Error("matching dev:ino was not the journal")
	}
	for _, bad := range []string{"", "garbage", "1:2", strconv.FormatUint(dev, 10) + ":" + strconv.FormatUint(ino+1, 10)} {
		if OnJournal(f, bad) {
			t.Errorf("%q matched", bad)
		}
	}
}

func TestFlagsReadEnvAndReject(t *testing.T) {
	t.Setenv("TNL_LOG_LEVEL", "debug")
	t.Setenv("TNL_LOG_FORMAT", "json")
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	c := AddFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	lg, err := c.Logger(&buf, "")
	if err != nil {
		t.Fatal(err)
	}
	lg.Debug("d")
	if !strings.HasPrefix(buf.String(), "{") {
		t.Fatalf("env did not select json debug: %q", buf.String())
	}

	// A flag beats the variable.
	fs = flag.NewFlagSet("x", flag.ContinueOnError)
	c = AddFlags(fs)
	_ = fs.Parse([]string{"--log-level", "error", "--log-format", "text"})
	buf.Reset()
	lg, _ = c.Logger(&buf, "")
	lg.Warn("w")
	if buf.Len() != 0 {
		t.Fatalf("flag did not win: %q", buf.String())
	}

	for _, args := range [][]string{{"--log-level", "loud"}, {"--log-format", "xml"}} {
		fs = flag.NewFlagSet("x", flag.ContinueOnError)
		c = AddFlags(fs)
		_ = fs.Parse(args)
		if _, err := c.Logger(&buf, ""); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestDefaultsAreInfoAndText(t *testing.T) {
	t.Setenv("TNL_LOG_LEVEL", "")
	t.Setenv("TNL_LOG_FORMAT", "")
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	c := AddFlags(fs)
	_ = fs.Parse(nil)
	var buf bytes.Buffer
	lg, err := c.Logger(&buf, "")
	if err != nil {
		t.Fatal(err)
	}
	lg.Debug("no")
	lg.Info("yes")
	if !strings.Contains(buf.String(), "level=info") || strings.Contains(buf.String(), "no") {
		t.Fatalf("got %q", buf.String())
	}
}

func TestWireGuardLoggerSendsBothToDebug(t *testing.T) {
	var buf bytes.Buffer
	l := WireGuardLogger(New(Options{Level: slog.LevelDebug, Out: &buf}))
	l.Verbosef("peer %d up", 1)
	l.Errorf("bad %s", "x")
	out := buf.String()
	if strings.Count(out, "level=debug") != 2 || !strings.Contains(out, "peer 1 up") || !strings.Contains(out, "bad x") {
		t.Fatalf("got %q", out)
	}
}

func TestWireGuardLoggerIsSilentAndNonNilAboveDebug(t *testing.T) {
	var buf bytes.Buffer
	l := WireGuardLogger(New(Options{Level: slog.LevelInfo, Out: &buf}))
	if l.Verbosef == nil || l.Errorf == nil {
		t.Fatal("nil log function panics wireguard-go")
	}
	l.Verbosef("x")
	l.Errorf("y")
	if buf.Len() != 0 {
		t.Fatalf("got %q", buf.String())
	}
	var _ *device.Logger = l
}
