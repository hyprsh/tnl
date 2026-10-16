package server_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	return startWith(t, version, actor, edgeResolver())
}

// startWith is start with an injected resolver for the DNS checks.
func startWith(t *testing.T, version string, actor api.Actor, res server.Resolver) *fixture {
	t.Helper()
	return startConfigured(t, version, actor, func(c *server.Config) { c.Resolver = res })
}

// startConfigured is start with a hook to set Config fields.
func startConfigured(t *testing.T, version string, actor api.Actor, set func(*server.Config)) *fixture {
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
	cfg := server.Config{Store: store, Log: lg, Version: version, Build: "full", Revision: "abc123", Role: "edge", Address: "tnl.example.com", WireGuardPort: 51820, StateDir: dir}
	set(&cfg)
	srv := server.New(cfg)
	ln, err := api.ListenSocket(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln, actor) }()
	t.Cleanup(func() { cancel(); <-done })
	return &fixture{dir: dir, log: buf, store: store, client: &api.Client{StateDir: dir, Version: version}}
}

func addZone(c *api.Client, name string) (api.ZoneAddReply, error) {
	return api.Call[api.ZoneAddReply](context.Background(), c, http.MethodPost, api.PathZones, api.ZoneAddRequest{Name: name})
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
	if z.ZoneInfo != (api.ZoneInfo{Name: "example.com", Mode: "per-hostname"}) || z.Warnings.Warnings != nil {
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
	_, err := api.Call[api.ZoneList](context.Background(), c, http.MethodGet, api.PathZones, nil)
	want := "this `tnl` is 1.2.4 but the running Edge is 1.2.3; restart it (`sudo systemctl restart tnl-edge`)"
	if err == nil || err.Error() != want {
		t.Fatalf("err=%v", err)
	}
}

func TestInfoReportsRoleVersionBuildAndRevision(t *testing.T) {
	f := start(t, "1.2.3", api.ActorLocal)
	info, err := api.Call[api.Info](context.Background(), f.client, http.MethodGet, api.PathInfo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info != (api.Info{Role: "edge", Version: "1.2.3", Build: "full", Revision: "abc123"}) {
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
	if _, err := api.ListenSocket(f.dir); err == nil {
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
	ln2, err := api.ListenSocket(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln2.Close()
}

// fakeResolver answers from a table; a name not in it is NXDOMAIN. Names under
// block, if set, wait for it to close.
type fakeResolver struct {
	hosts map[string][]netip.Addr
	block chan struct{}
}

func (r *fakeResolver) LookupNetIP(ctx context.Context, _, host string) ([]netip.Addr, error) {
	if r.block != nil && strings.HasSuffix(host, ".slow.test") {
		select {
		case <-r.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if a, ok := r.hosts[host]; ok {
		return a, nil
	}
	if strings.HasSuffix(host, ".points-here.test") || strings.HasSuffix(host, ".example.com") {
		return r.hosts["tnl.example.com"], nil
	}
	if strings.HasSuffix(host, ".points-away.test") {
		return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func edgeResolver() *fakeResolver {
	return &fakeResolver{hosts: map[string][]netip.Addr{"tnl.example.com": {netip.MustParseAddr("198.51.100.7")}}}
}

func logLines(f *fixture, substr string) []string {
	var out []string
	for _, l := range strings.Split(f.log.String(), "\n") {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return out
}

func TestZoneAddWarnsWhenNamesPointElsewhereOrNowhere(t *testing.T) {
	for _, zone := range []string{"points-away.test", "nowhere.test"} {
		f := startWith(t, "1.2.3", api.ActorLocal, edgeResolver())
		z, err := addZone(f.client, zone)
		if err != nil {
			t.Fatal(err)
		}
		if len(z.Warnings.Warnings) != 1 || !strings.Contains(z.Warnings.Warnings[0], zone) || !strings.Contains(z.Warnings.Warnings[0], "tnl.example.com") {
			t.Fatalf("%s: warnings %q", zone, z.Warnings.Warnings)
		}
		if got := f.store.Snapshot().Zones; len(got) != 1 {
			t.Fatalf("%s: the Zone was not saved: %+v", zone, got)
		}
		lines := logLines(f, "level=warn")
		if len(lines) != 1 || !strings.Contains(lines[0], "actor=local") || !strings.Contains(lines[0], z.Warnings.Warnings[0]) {
			t.Fatalf("%s: warn lines %q", zone, lines)
		}
	}
}

func TestZoneAddNoWarningWhenNamesPointAtTheEdge(t *testing.T) {
	f := startWith(t, "1.2.3", api.ActorLocal, edgeResolver())
	z, err := addZone(f.client, "points-here.test")
	if err != nil {
		t.Fatal(err)
	}
	if z.Warnings.Warnings != nil || len(logLines(f, "level=warn")) != 0 {
		t.Fatalf("warnings %q, log %s", z.Warnings.Warnings, f.log.String())
	}
}

func TestZoneAddResolvesAfterTheLock(t *testing.T) {
	res := edgeResolver()
	res.block = make(chan struct{})
	f := startWith(t, "1.2.3", api.ActorLocal, res)
	done := make(chan error, 1)
	go func() { _, err := addZone(f.client, "slow.test"); done <- err }()
	// Wait until the Zone is saved: the resolver is then running, blocked.
	deadline := time.Now().Add(5 * time.Second)
	for len(f.store.Snapshot().Zones) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the Zone was never saved")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A concurrent change must not wait for the resolver.
	other := make(chan error, 1)
	go func() { _, err := addZone(f.client, "points-here.test"); other <- err }()
	select {
	case err := <-other:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a concurrent change waited for the resolver")
	}
	close(res.block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
