package server_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/state"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type fixture struct {
	dir    string
	log    *syncBuf
	client *api.Client
	store  *state.Store
}

// start serves the API on tnl.sock in a fresh state directory, with the given
// actor on the listener.
func start(t *testing.T, version string, actor api.Actor) *fixture {
	t.Helper()
	// Unix socket paths are short on macOS, so keep the directory name short.
	dir, err := os.MkdirTemp("", "tnl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	buf := &syncBuf{}
	lg := logx.New(logx.Options{Level: slog.LevelInfo, Out: buf})
	store, err := state.Open(dir, lg)
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(server.Config{Store: store, Log: lg, Version: version, Build: "abc", Role: "edge", Address: "tnl.example.com", WireGuardPort: 51820})
	ln, err := server.ListenSocket(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln, actor) }()
	t.Cleanup(func() { cancel(); <-done })
	return &fixture{dir: dir, log: buf, store: store, client: &api.Client{StateDir: dir, Version: version}}
}

func addZone(c *api.Client, name string) (api.ZoneInfo, error) {
	return api.Call[api.ZoneInfo](context.Background(), c, http.MethodPost, api.PathZones, api.ZoneAddRequest{Name: name})
}

func rawClient(dir string) *http.Client {
	return &http.Client{Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, api.SocketName))
	}}}
}

func TestSocketHasMode0600(t *testing.T) {
	f := start(t, "1.2.3", api.ActorLocal)
	fi, err := os.Stat(filepath.Join(f.dir, api.SocketName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("mode %v", fi.Mode())
	}
}

func TestZoneAddThenLsAndLogsActorLocal(t *testing.T) {
	f := start(t, "1.2.3", api.ActorLocal)
	z, err := addZone(f.client, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if z != (api.ZoneInfo{Name: "example.com", Mode: "per-hostname"}) {
		t.Fatalf("%+v", z)
	}
	list, err := api.Call[api.ZoneList](context.Background(), f.client, http.MethodGet, api.PathZones, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Zones) != 1 || list.Zones[0].Name != "example.com" {
		t.Fatalf("%+v", list)
	}
	if got := f.store.Snapshot().Zones; len(got) != 1 || got[0].Mode != state.PerHostname {
		t.Fatalf("store has %+v", got)
	}
	var line string
	for _, l := range strings.Split(f.log.String(), "\n") {
		if strings.Contains(l, "zone added") {
			line = l
		}
	}
	for _, want := range []string{"level=info", "actor=local", "example.com"} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line %q lacks %q", line, want)
		}
	}
}

func TestZoneAddRefusals(t *testing.T) {
	f := start(t, "1.2.3", api.ActorLocal)
	if _, err := addZone(f.client, "example.com"); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"example.com":   "already",
		"EXAMPLE.com":   "already",
		"":              "name",
		"*.example.com": "wildcard",
		"exa mple.com":  "invalid",
		"-a.com":        "invalid",
		"a..com":        "invalid",
	} {
		_, err := addZone(f.client, name)
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Status < 400 || !strings.Contains(ae.Message, want) {
			t.Errorf("%q: err=%v, want it to contain %q", name, err, want)
		}
	}
	if n := len(f.store.Snapshot().Zones); n != 1 {
		t.Fatalf("%d zones", n)
	}
}

func TestEveryResponseCarriesTnlVersion(t *testing.T) {
	f := start(t, "1.2.3", api.ActorLocal)
	hc := rawClient(f.dir)
	for _, tc := range []struct{ method, path string }{
		{"GET", api.PathZones}, {"GET", api.PathInfo}, {"GET", "/nope"}, {"DELETE", api.PathZones},
	} {
		req, _ := http.NewRequest(tc.method, "http://tnl"+tc.path, nil)
		req.Header.Set(api.VersionHeader, "1.2.3")
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := resp.Header.Get(api.VersionHeader); got != "1.2.3" {
			t.Errorf("%s %s: Tnl-Version=%q", tc.method, tc.path, got)
		}
	}
}

func TestUnknownFieldRefusesTheWholeRequest(t *testing.T) {
	f := start(t, "1.2.3", api.ActorLocal)
	resp, err := rawClient(f.dir).Post("http://tnl"+api.PathZones, "application/json", strings.NewReader(`{"name":"example.com","new_opt":true}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 400 || !strings.Contains(string(body), "new_opt") || !strings.Contains(string(body), "1.2.3") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if n := len(f.store.Snapshot().Zones); n != 0 {
		t.Fatalf("zone saved despite the unknown field")
	}
}

func TestClientRefusesADifferentVersion(t *testing.T) {
	f := start(t, "1.2.3", api.ActorLocal)
	c := &api.Client{StateDir: f.dir, Version: "1.2.4"}
	_, err := api.Call[api.Info](context.Background(), c, http.MethodGet, api.PathInfo, nil)
	if err == nil || !strings.Contains(err.Error(), "1.2.3") || !strings.Contains(err.Error(), "1.2.4") {
		t.Fatalf("err=%v", err)
	}
}

func TestInfoReportsRoleVersionAndBuild(t *testing.T) {
	f := start(t, "1.2.3", api.ActorLocal)
	info, err := api.Call[api.Info](context.Background(), f.client, http.MethodGet, api.PathInfo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info != (api.Info{Role: "edge", Version: "1.2.3", Build: "abc"}) {
		t.Fatalf("%+v", info)
	}
}

func TestNoEdgeRunningIsAClearError(t *testing.T) {
	c := &api.Client{StateDir: t.TempDir(), Version: "1.2.3"}
	_, err := api.Call[api.ZoneList](context.Background(), c, http.MethodGet, api.PathZones, nil)
	if err == nil || !strings.Contains(err.Error(), "no Edge is running") {
		t.Fatalf("err=%v", err)
	}
}

func TestOtherActorsCannotAddZonesButCanListThem(t *testing.T) {
	f := start(t, "1.2.3", api.OutpostActor("nas"))
	if _, err := addZone(f.client, "example.com"); err == nil {
		t.Fatal("outpost added a zone")
	}
	if _, err := api.Call[api.ZoneList](context.Background(), f.client, http.MethodGet, api.PathZones, nil); err != nil {
		t.Fatal(err)
	}
}

func TestALiveSocketIsNotReplaced(t *testing.T) {
	f := start(t, "1.2.3", api.ActorLocal)
	if _, err := server.ListenSocket(f.dir); err == nil {
		t.Fatal("second listener on a live socket")
	}
}

func TestAStaleSocketIsReplaced(t *testing.T) {
	dir, err := os.MkdirTemp("", "tnl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, api.SocketName))
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close() // leaves the file behind, as a crash would
	ln2, err := server.ListenSocket(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln2.Close()
}
