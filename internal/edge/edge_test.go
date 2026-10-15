package edge

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/logx"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func run(t *testing.T, dir string) (*syncBuf, error) {
	t.Helper()
	buf := &syncBuf{}
	lg := logx.New(logx.Options{Level: slog.LevelInfo, Out: buf})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Address: "tnl.example.com", StateDir: dir, Version: "v9.9.9", Build: "abc123", Log: lg})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), "edge started") && time.Now().Before(deadline) {
		select {
		case err := <-done:
			return buf, err
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		return buf, err
	case <-time.After(2 * time.Second):
		t.Fatal("Run ignored cancellation")
		return buf, nil
	}
}

func TestStartLogsVersionAddressFingerprintAndCounts(t *testing.T) {
	dir := filepath.Join(shortDir(t), "state")
	buf, err := run(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, want := range []string{"created a new Edge", "key_fingerprint=SHA256:", "version=v9.9.9", "build=abc123", "address=tnl.example.com", "outposts=0", "routes=0", "zones=0"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err != nil {
		t.Error(err)
	}
}

func TestBadStateIsLoggedAtErrorAndRunFails(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"version": 42}`), 0o600)
	buf, err := run(t, dir)
	if err == nil {
		t.Fatal("started on a state it can't read")
	}
	if !strings.Contains(buf.String(), "level=error") || !strings.Contains(buf.String(), "version 42") {
		t.Errorf("log: %s", buf.String())
	}
}

// shortDir is a temporary directory with a path short enough for a Unix
// socket in it (104 bytes on macOS).
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tnl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestZonesSurviveARestart(t *testing.T) {
	dir := shortDir(t)
	c := &api.Client{StateDir: dir, Version: "v9.9.9"}

	serve := func(body func()) {
		t.Helper()
		buf := &syncBuf{}
		lg := logx.New(logx.Options{Level: slog.LevelInfo, Out: buf})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- Run(ctx, Config{Address: "tnl.example.com", StateDir: dir, Version: "v9.9.9", Build: "abc123", Log: lg})
		}()
		deadline := time.Now().Add(2 * time.Second)
		for !strings.Contains(buf.String(), "edge started") && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		body()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, api.SocketName)); !os.IsNotExist(err) {
			t.Errorf("socket left behind: %v", err)
		}
	}

	serve(func() {
		if _, err := api.Call[api.ZoneInfo](context.Background(), c, http.MethodPost, api.PathZones, api.ZoneAddRequest{Name: "example.com"}); err != nil {
			t.Fatal(err)
		}
	})
	serve(func() {
		list, err := api.Call[api.ZoneList](context.Background(), c, http.MethodGet, api.PathZones, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(list.Zones) != 1 || list.Zones[0].Name != "example.com" {
			t.Fatalf("%+v", list)
		}
	})
}
