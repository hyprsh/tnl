package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Caddy resolves its config directory while its package initialises, before
// main, and prints a JSON line on stderr if neither HOME nor XDG_CONFIG_HOME
// is set. Only a real process shows it. This one runs as the Edge unit does:
// no HOME, and XDG_CONFIG_HOME under its state directory. Every stderr line it
// writes must be a tnl line (time=... level=...), and Caddy's startup lines
// must come once, not twice.
func TestEdgeUnderTheUnitsEnvironmentWritesOnlyTnlLinesToStderr(t *testing.T) {
	bin := filepath.Join(skewEnv(t, "TNL_SKEW_DIR"), "tnl-a")
	webFile := skewEnv(t, "TNL_SKEW_WEB")
	httpPort, httpsPort, wgPort := freePort(t, false), freePort(t, true), freePort(t, true)
	web, _ := json.Marshal(map[string]any{
		"HTTPListen": fmt.Sprintf("127.0.0.1:%d", httpPort), "HTTPSListen": fmt.Sprintf("127.0.0.1:%d", httpsPort),
		"HTTPPort": httpPort, "HTTPSPort": httpsPort, "DNSPropagationTimeout": -1,
	})
	if err := os.WriteFile(webFile, web, 0o600); err != nil {
		t.Fatal(err)
	}
	short, err := os.MkdirTemp("", "tnle")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	stateDir := filepath.Join(short, "e")
	env := []string{"PATH=" + os.Getenv("PATH"), "XDG_CONFIG_HOME=" + filepath.Join(stateDir, "config"), "XDG_DATA_HOME=" + filepath.Join(short, "data")}

	p := startSkewProc(t, env, bin, "edge", "--address", "127.0.0.1", "--state-dir", stateDir, "--wireguard-port", fmt.Sprint(wgPort), "--log-level", "info")
	waitFor(t, "the Edge's socket", 30*time.Second, func() bool {
		select {
		case <-p.done:
			t.Fatalf("the Edge exited:\n%s", p.out)
		default:
		}
		_, err := os.Stat(filepath.Join(stateDir, "tnl.sock"))
		return err == nil
	})
	time.Sleep(time.Second) // Caddy's background lines
	p.stop()

	// The one exception is Caddy's "redirected default logger" JSON line, written
	// once at each start before the redirect takes effect (listed in the spec).
	var redirected int
	for _, l := range strings.Split(strings.TrimSpace(p.out.String()), "\n") {
		switch {
		case strings.Contains(l, `"msg":"redirected default logger"`):
			redirected++
		case !strings.HasPrefix(l, "time="):
			t.Errorf("stderr line that is not a tnl line: %q", l)
		}
	}
	if redirected != 1 {
		t.Errorf("%d \"redirected default logger\" lines, want 1:\n%s", redirected, p.out)
	}
}
