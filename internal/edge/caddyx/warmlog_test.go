package caddyx

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/edge/routes"
	"github.com/hyprsh/tnl/internal/logx"
)

type resetBuf struct{ syncBuf }

func (r *resetBuf) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.b.Reset()
}

// Caddy's startup lines come once on a cold start with certificates in storage,
// although the first load is the TLS app alone and the real config follows.
func TestAColdStartWithStoredCertificatesLogsCaddysStartupLinesOnce(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_DATA_HOME", "")
	httpsPort, httpPort := freePort(t), freePort(t)
	buf := new(resetBuf)
	lg := logx.New(logx.Options{Level: slog.LevelInfo, Out: buf})
	rt := &Runtime{Table: new(routes.Mem), Outposts: new(routes.MemOutposts), Log: lg}
	Register("dup", rt)
	cfg := Config{
		EdgeID: "dup", StateDir: state,
		HTTPListen: fmt.Sprintf("127.0.0.1:%d", httpPort), HTTPSListen: fmt.Sprintf("127.0.0.1:%d", httpsPort),
		HTTPPort: httpPort, HTTPSPort: httpsPort,
	}
	if err := Start(cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := rt.Certs().Obtain(ctx, "a.dup.test"); err != nil {
		t.Fatal(err)
	}
	Stop()
	buf.Reset()
	if err := Start(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Stop() })
	time.Sleep(500 * time.Millisecond)
	if !strings.Contains(strings.Join(buf.lines(), "\n"), "started background certificate maintenance") {
		t.Error("the cache's own startup line is missing")
	}
	seen := map[string]int{}
	for _, l := range buf.lines() {
		if i := strings.Index(l, "msg="); i >= 0 {
			seen[l[i:]]++
		}
	}
	for l, n := range seen {
		if n > 1 {
			t.Errorf("%d times: %s", n, l)
		}
	}
}
