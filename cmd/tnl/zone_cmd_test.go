//go:build !outpost

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
	"github.com/hyprsh/tnl/internal/edge/routes"
	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/state"
)

// runningEdge serves the API in a fresh state directory and points the CLI at
// it through TNL_STATE_DIR.
// edgeTestDialer is the test dialer runningEdge gives the Edge; a test sets it
// before starting one.
var edgeTestDialer routes.TestDialer

// edgeWildcards is the wildcard-Zone side runningEdge gives the Edge; nil
// refuses wildcard Zones.
var edgeWildcards server.Wildcards

func runningEdge(t *testing.T) {
	runningEdgeWith(t, fakeDNS{"zone.test": true, "example.com": true, "b.example.com": true})
}

// fakeDNS answers tnl.example.com with the Edge's IP; a name under a key set
// to true points there too, one set to false at another address, others
// nowhere.
type fakeDNS map[string]bool

func (f fakeDNS) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if host == "tnl.example.com" {
		return []netip.Addr{netip.MustParseAddr("198.51.100.7")}, nil
	}
	for zone, here := range f {
		if strings.HasSuffix(host, "."+zone) {
			if here {
				return []netip.Addr{netip.MustParseAddr("198.51.100.7")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
		}
	}
	return nil, errors.New("no such host")
}

// runningEdgeWith is runningEdge with the DNS the Edge's checks see.
func runningEdgeWith(t *testing.T, res server.Resolver) {
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
	v, rev := versionInfo()
	srv := server.New(server.Config{Store: store, Log: lg, Version: v, Build: buildName, Revision: rev, Role: "edge", Address: "tnl.example.com", WireGuardPort: 51820, Resolver: res, StateDir: dir, TestDialer: edgeTestDialer, Wildcards: edgeWildcards})
	ln, err := api.ListenSocket(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln, api.ActorLocal) }()
	t.Cleanup(func() { cancel(); <-done })
	t.Setenv("TNL_STATE_DIR", dir)
}

// tnlBoth is tnl that also returns what the command wrote to stderr.
func tnlBoth(args ...string) (out, errOut string, code int, err error) {
	var buf, ebuf bytes.Buffer
	old, olde := stdout, stderr
	stdout, stderr = &buf, &ebuf
	defer func() { stdout, stderr = old, olde }()
	err = commands[args[0]](args[1:])
	return buf.String(), ebuf.String(), exitCode(err), err
}

func TestZoneAddWarnsOnStderrAndExitsZero(t *testing.T) {
	runningEdgeWith(t, fakeDNS{"elsewhere.test": false})
	for _, zone := range []string{"elsewhere.test", "nowhere.test"} {
		out, errOut, code, err := tnlBoth("zone", "add", zone)
		if err != nil || code != 0 || !strings.Contains(out, "added Zone "+zone) {
			t.Fatalf("%s: %q %v %d", zone, out, err, code)
		}
		if !strings.HasPrefix(errOut, "warning: ") || !strings.Contains(errOut, zone) || strings.Count(errOut, "\n") != 1 {
			t.Fatalf("%s: stderr %q", zone, errOut)
		}
		if strings.Contains(out, "warning") {
			t.Fatalf("%s: warning on stdout: %q", zone, out)
		}
	}
	ls, _, err := tnl("zone", "ls")
	if err != nil || !strings.Contains(ls, "elsewhere.test") || !strings.Contains(ls, "nowhere.test") {
		t.Fatalf("%q %v", ls, err)
	}
}

func TestZoneAddPrintsNoWarningWhenNamesPointAtTheEdge(t *testing.T) {
	runningEdge(t)
	_, errOut, code, err := tnlBoth("zone", "add", "zone.test")
	if err != nil || code != 0 || errOut != "" {
		t.Fatalf("%q %v %d", errOut, err, code)
	}
}

func TestZoneAddThenLs(t *testing.T) {
	runningEdge(t)
	out, code, err := tnl("zone", "add", "example.com")
	if err != nil || code != 0 || !strings.Contains(out, "example.com") {
		t.Fatalf("%q %v %d", out, err, code)
	}
	if _, _, err := tnl("zone", "add", "b.example.com"); err != nil {
		t.Fatal(err)
	}
	out, _, err = tnl("zone", "ls")
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
		_, code, err := tnl("zone", "add", name)
		if err == nil || code != 1 {
			t.Errorf("%q: err=%v code=%d", name, err, code)
		}
	}
}

func TestStateDirFlagBeatsEnvironment(t *testing.T) {
	runningEdge(t)
	dir := os.Getenv("TNL_STATE_DIR")
	t.Setenv("TNL_STATE_DIR", t.TempDir()) // no Edge here
	if _, _, err := tnl("zone", "ls"); err == nil {
		t.Fatal("used the environment's directory")
	}
	if _, _, err := tnl("zone", "ls", "--state-dir", dir); err != nil {
		t.Fatal(err)
	}
}

func TestNoEdgeRunningExitsOneWithAClearMessage(t *testing.T) {
	t.Setenv("TNL_STATE_DIR", t.TempDir())
	for _, args := range [][]string{{"zone", "ls"}, {"zone", "add", "example.com"}} {
		_, code, err := tnl(args...)
		if code != 1 || err == nil || !strings.Contains(err.Error(), "no Edge is running") {
			t.Errorf("%v: err=%v code=%d", args, err, code)
		}
	}
}
