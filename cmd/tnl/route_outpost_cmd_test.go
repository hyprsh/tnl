package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
)

// fakeOutpost answers on a tnl.sock as an Outpost's relay does: info with the
// role "outpost", and every other call recorded and answered with a Route.
type fakeOutpost struct {
	mu   sync.Mutex
	reqs []string
}

func (f *fakeOutpost) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...)
}

func runningOutpost(t *testing.T) *fakeOutpost {
	t.Helper()
	dir, err := os.MkdirTemp("", "tnl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, api.SocketName))
	if err != nil {
		t.Fatal(err)
	}
	v, rev := versionInfo()
	f := &fakeOutpost{}
	hs := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(api.VersionHeader, "edge-version")
		if r.URL.Path == api.PathInfo {
			json.NewEncoder(w).Encode(api.Info{Role: api.RoleOutpost, Version: v, Build: "outpost", Revision: rev})
			return
		}
		f.mu.Lock()
		f.reqs = append(f.reqs, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		json.NewEncoder(w).Encode(api.RouteInfo{Hostname: "a.example.com", Outpost: "nas", Service: "http://x:1"})
	})}
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close() })
	t.Setenv("TNL_STATE_DIR", dir)
	return f
}

func TestRouteCommandsOnAnOutpostGoThroughTheRelayWithoutOutpost(t *testing.T) {
	f := runningOutpost(t)
	out, _, err := tnl("route", "add", "a.example.com", "http://x:1")
	if err != nil || !strings.Contains(out, "through Outpost nas") {
		t.Fatalf("%q %v", out, err)
	}
	if _, _, err := tnl("route", "edit", "a.example.com", "--host-header", "h"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tnl("route", "rm", "a.example.com"); err != nil {
		t.Fatal(err)
	}
	want := "POST /v1/routes,POST /v1/routes/edit,POST /v1/routes/rm"
	if got := strings.Join(f.seen(), ","); got != want {
		t.Fatalf("relay saw %s", got)
	}
}

func TestOutpostFlagIsRefusedOnAnOutpostBeforeAnythingIsSent(t *testing.T) {
	f := runningOutpost(t)
	for _, args := range [][]string{
		{"route", "add", "a.example.com", "http://x:1", "--outpost", "nas"},
		{"route", "edit", "a.example.com", "--outpost", "nas"},
	} {
		_, _, err := tnl(args...)
		if err == nil || !strings.Contains(err.Error(), "--outpost doesn't exist on an Outpost") {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if n := len(f.seen()); n != 0 {
		t.Fatalf("%d requests were sent", n)
	}
}
