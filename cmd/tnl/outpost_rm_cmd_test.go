//go:build !outpost

package main

import (
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/token"
)

func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestOutpostAddAndRekeyPrintTheTokenThenTheInstallLinePinnedToTheVersion(t *testing.T) {
	withVersion(t, "1.2.3")
	runningEdge(t)
	for _, args := range [][]string{{"outpost", "add", "laptop"}, {"outpost", "rekey", "laptop"}} {
		out, errOut, code, err := tnlBoth(args...)
		if err != nil || code != 0 || errOut != "" {
			t.Fatalf("%v: %q %q %v %d", args, out, errOut, err, code)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 2 {
			t.Fatalf("%v: stdout %q", args, out)
		}
		if _, err := token.Decode(lines[0]); err != nil {
			t.Fatalf("first line is not a token: %v", err)
		}
		want := "curl -fsSL https://github.com/hyprsh/tnl/releases/download/v1.2.3/install.sh | sudo sh -s -- outpost --token " + lines[0]
		if lines[1] != want {
			t.Fatalf("install line %q, want %q", lines[1], want)
		}
	}
}

func TestInstallLineOfADevBuildIsTheLatestOneAndSaysItIsNotPinned(t *testing.T) {
	withVersion(t, "dev")
	runningEdge(t)
	out, errOut, code, err := tnlBoth("outpost", "add", "laptop")
	if err != nil || code != 0 {
		t.Fatalf("%v %d", err, code)
	}
	if !strings.Contains(out, "https://github.com/hyprsh/tnl/releases/latest/download/install.sh | sudo sh -s -- outpost --token ") {
		t.Fatalf("stdout %q", out)
	}
	if !strings.Contains(errOut, "not pinned") {
		t.Fatalf("stderr %q", errOut)
	}
}

func TestOutpostRmRefusesWithRoutesListsThemAndWithRoutesRemoves(t *testing.T) {
	runningEdge(t)
	for _, args := range [][]string{{"zone", "add", "example.com"}, {"outpost", "add", "nas"}, {"route", "add", "a.example.com", "http://localhost:1", "--outpost", "nas"}} {
		if _, _, err := tnl(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	out, code, err := tnl("outpost", "rm", "nas")
	if err == nil || code != 1 || out != "" || !strings.Contains(err.Error(), "a.example.com") {
		t.Fatalf("%q %d %v", out, code, err)
	}
	out, code, err = tnl("outpost", "rm", "nas", "--with-routes")
	if err != nil || code != 0 || !strings.Contains(out, "removed Outpost nas") || !strings.Contains(out, "a.example.com") {
		t.Fatalf("%q %d %v", out, code, err)
	}
	if _, code, err := tnl("outpost", "rm", "nas"); err == nil || code != 1 {
		t.Fatalf("a second rm: %d %v", code, err)
	}
	if _, code, err := tnl("outpost", "rm"); err == nil || code != 1 {
		t.Fatalf("no name: %d %v", code, err)
	}
}

func TestOutpostRekeyOfAnUnknownOutpostFails(t *testing.T) {
	runningEdge(t)
	if out, code, err := tnl("outpost", "rekey", "ghost"); err == nil || code != 1 || out != "" {
		t.Fatalf("%q %d %v", out, code, err)
	}
	if _, code, err := tnl("outpost", "rekey", "ghost", "--ttl", "soon"); err == nil || code != 1 {
		t.Fatalf("bad ttl: %d %v", code, err)
	}
}
