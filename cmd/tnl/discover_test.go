package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// socketDir makes a state directory with a listening tnl.sock. Unix socket
// paths are length-limited, so it avoids t.TempDir.
func socketDir(t *testing.T) string {
	t.Helper()
	dir := emptyDir(t)
	ln, err := net.Listen("unix", filepath.Join(dir, "tnl.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return dir
}

func emptyDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tnl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// candidates makes discovery search exactly dirs, and clears the pinned directory.
func candidates(t *testing.T, dirs ...string) {
	t.Helper()
	old := socketDirs
	socketDirs = func() []string { return dirs }
	t.Cleanup(func() { socketDirs = old })
	t.Setenv("TNL_STATE_DIR", "")
}

func TestDiscoverPinnedDirIgnoresTheSearch(t *testing.T) {
	pinned, other := socketDir(t), socketDir(t)
	candidates(t, other)
	got, err := discoverStateDir(pinned)
	if err != nil || got != pinned {
		t.Fatalf("got %q, %v; want %q", got, err, pinned)
	}
}

func TestDiscoverPinnedByEnvironment(t *testing.T) {
	pinned, other := socketDir(t), socketDir(t)
	candidates(t, other)
	t.Setenv("TNL_STATE_DIR", pinned)
	c := newClientCommand("x")
	if _, err := c.parse(nil); err != nil || c.stateDir != pinned {
		t.Fatalf("stateDir %q, %v; want %q", c.stateDir, err, pinned)
	}
}

func TestDiscoverPinnedFlagWinsOverEnvironment(t *testing.T) {
	flagged, env := socketDir(t), socketDir(t)
	candidates(t)
	t.Setenv("TNL_STATE_DIR", env)
	c := newClientCommand("x")
	if _, err := c.parse([]string{"--state-dir", flagged}); err != nil || c.stateDir != flagged {
		t.Fatalf("stateDir %q, %v; want %q", c.stateDir, err, flagged)
	}
}

func TestDiscoverUsesTheOneLiveSocket(t *testing.T) {
	live := socketDir(t)
	candidates(t, emptyDir(t), live, filepath.Join(os.TempDir(), "tnl-no-such-dir"))
	c := newClientCommand("x")
	if _, err := c.parse(nil); err != nil || c.stateDir != live {
		t.Fatalf("stateDir %q, %v; want %q", c.stateDir, err, live)
	}
}

func TestDiscoverSearchOrderPicksTheFirstLiveWhenOthersAreDead(t *testing.T) {
	first := socketDir(t)
	candidates(t, first, emptyDir(t))
	if got, err := discoverStateDir(""); err != nil || got != first {
		t.Fatalf("got %q, %v; want %q", got, err, first)
	}
}

func TestDiscoverSkipsAStaleSocketFile(t *testing.T) {
	stale := emptyDir(t)
	ln, err := net.Listen("unix", filepath.Join(stale, "tnl.sock"))
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close() // the file stays, nothing listens
	live := socketDir(t)
	candidates(t, stale, live)
	if got, err := discoverStateDir(""); err != nil || got != live {
		t.Fatalf("got %q, %v; want %q", got, err, live)
	}
}

func TestDiscoverTwoLiveSocketsAreRefusedWithTheList(t *testing.T) {
	a, b := socketDir(t), socketDir(t)
	candidates(t, a, b)
	_, err := discoverStateDir("")
	if err == nil {
		t.Fatal("two live sockets were accepted")
	}
	for _, want := range []string{filepath.Join(a, "tnl.sock"), filepath.Join(b, "tnl.sock"), "--state-dir"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if exitCode(err) != 1 {
		t.Errorf("exit %d, want 1", exitCode(err))
	}
}

func TestDiscoverUnopenableSocketSaysRunWithSudo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can open any socket")
	}
	dir := socketDir(t)
	sock := filepath.Join(dir, "tnl.sock")
	if err := os.Chmod(sock, 0); err != nil {
		t.Fatal(err)
	}
	candidates(t, emptyDir(t), dir)
	_, err := discoverStateDir("")
	if err == nil || !strings.Contains(err.Error(), "run with sudo") || !strings.Contains(err.Error(), sock) {
		t.Fatalf("got %v; want a \"run with sudo\" naming %s", err, sock)
	}
}

func TestDiscoverNoneLiveExitsOne(t *testing.T) {
	candidates(t, emptyDir(t), emptyDir(t))
	_, err := discoverStateDir("")
	if err == nil || !strings.Contains(err.Error(), "no Edge or Outpost is running") {
		t.Fatalf("got %v", err)
	}
	if exitCode(err) != 1 {
		t.Errorf("exit %d, want 1", exitCode(err))
	}
}
