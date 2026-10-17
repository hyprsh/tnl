package caddyx

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/hyprsh/tnl/internal/edge/routes"
)

// A first load that fails must not leave Caddy's default logger buffering, or
// every later Caddy entry would vanish.
func TestAFailedFirstLoadLeavesNoCaddyEntryBuffered(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	rt := &Runtime{Table: new(routes.Mem), Outposts: new(routes.MemOutposts)}
	Register("failfirst", rt)
	if activeTLS() != nil {
		Stop()
	}
	err = Start(Config{
		EdgeID: "failfirst", StateDir: t.TempDir(),
		HTTPListen: fmt.Sprintf("127.0.0.1:%d", port), HTTPSListen: fmt.Sprintf("127.0.0.1:%d", port),
		HTTPPort: port, HTTPSPort: port,
	})
	t.Cleanup(func() { Stop() })
	if err == nil {
		t.Fatal("the load did not fail")
	}
	if c := fmt.Sprintf("%T", caddy.Log().Core()); strings.Contains(c, "LogBufferCore") {
		t.Errorf("Caddy's default logger still buffers after a failed first load: %s", c)
	}
}
