//go:build !outpost

package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/state"
)

// runningEdge serves the API in a fresh state directory and points the CLI at
// it through TNL_STATE_DIR.
func runningEdge(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "tnl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	lg := logx.New(logx.Options{Level: slog.LevelInfo, Out: io.Discard})
	store, err := state.Open(dir, lg)
	if err != nil {
		t.Fatal(err)
	}
	v, b := versionInfo()
	srv := server.New(server.Config{Store: store, Log: lg, Version: v, Build: b, Role: "edge", Address: "tnl.example.com", WireGuardPort: 51820})
	ln, err := server.ListenSocket(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln, api.ActorLocal) }()
	t.Cleanup(func() { cancel(); <-done })
	t.Setenv("TNL_STATE_DIR", dir)
}

// tnl runs a command the way main does and returns what it printed, the error
// it would print and its exit code.
func tnl(args ...string) (out string, err error, code int) {
	var buf bytes.Buffer
	old := stdout
	stdout = &buf
	defer func() { stdout = old }()
	err = commands[args[0]](args[1:])
	return buf.String(), err, exitCode(err)
}

func TestZoneAddThenLs(t *testing.T) {
	runningEdge(t)
	out, err, code := tnl("zone", "add", "example.com")
	if err != nil || code != 0 || !strings.Contains(out, "example.com") {
		t.Fatalf("%q %v %d", out, err, code)
	}
	if _, err, _ := tnl("zone", "add", "b.example.com"); err != nil {
		t.Fatal(err)
	}
	out, err, _ = tnl("zone", "ls")
	if err != nil {
		t.Fatal(err)
	}
	want := "ZONE           MODE          PROVIDER\n" +
		"b.example.com  per-hostname  -\n" +
		"example.com    per-hostname  -\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestZoneAddRefusalsExitOne(t *testing.T) {
	runningEdge(t)
	tnl("zone", "add", "example.com")
	for _, name := range []string{"example.com", "not a zone", "*.example.com"} {
		_, err, code := tnl("zone", "add", name)
		if err == nil || code != 1 {
			t.Errorf("%q: err=%v code=%d", name, err, code)
		}
	}
}

func TestStateDirFlagBeatsEnvironment(t *testing.T) {
	runningEdge(t)
	dir := os.Getenv("TNL_STATE_DIR")
	t.Setenv("TNL_STATE_DIR", t.TempDir()) // no Edge here
	if _, err, _ := tnl("zone", "ls"); err == nil {
		t.Fatal("used the environment's directory")
	}
	if _, err, _ := tnl("zone", "ls", "--state-dir", dir); err != nil {
		t.Fatal(err)
	}
}

func TestNoEdgeRunningExitsOneWithAClearMessage(t *testing.T) {
	t.Setenv("TNL_STATE_DIR", t.TempDir())
	for _, args := range [][]string{{"zone", "ls"}, {"zone", "add", "example.com"}} {
		_, err, code := tnl(args...)
		if code != 1 || err == nil || !strings.Contains(err.Error(), "no Edge is running") {
			t.Errorf("%v: err=%v code=%d", args, err, code)
		}
	}
}

func TestStateDirDefaultsToVarLibTnl(t *testing.T) {
	t.Setenv("TNL_STATE_DIR", "")
	c := newClientCommand("zone ls")
	if _, err := c.parse(nil); err != nil || c.stateDir != "/var/lib/tnl" {
		t.Fatalf("%v %q", err, c.stateDir)
	}
}
