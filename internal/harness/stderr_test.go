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
// no HOME, and XDG_CONFIG_HOME under its state directory. It is the second
// start of an Edge that holds a certificate in storage, so the warm path runs
// (a TLS-only load, then the full one). Every stderr line it writes must be a
// tnl line (time=... level=...), and Caddy's startup lines must come once, not
// twice.
func TestEdgeUnderTheUnitsEnvironmentWritesOnlyTnlLinesToStderr(t *testing.T) {
	bin := filepath.Join(skewEnv(t, "TNL_SKEW_DIR"), "tnl-a")
	webFile := skewEnv(t, "TNL_SKEW_WEB")
	pebble := StartPebble(t, PebbleOptions{})
	httpPort, httpsPort, wgPort := freePort(t, false), freePort(t, true), freePort(t, true)
	web, _ := json.Marshal(map[string]any{
		"HTTPListen": fmt.Sprintf("127.0.0.1:%d", httpPort), "HTTPSListen": fmt.Sprintf("127.0.0.1:%d", httpsPort),
		"HTTPPort": httpPort, "HTTPSPort": httpsPort, "DNSPropagationTimeout": -1,
		"ACMECA": pebble.DirectoryURL, "ACMEEmail": "ops@example.com", "ACMETrustedRoots": []string{pebble.certFile},
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

	start := func() *skewProc {
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
		return p
	}
	cli := func(args ...string) {
		t.Helper()
		var out string
		var err error
		for range 50 { // the socket can be there a moment before it answers
			if out, err = skewCLI(bin, stateDir, args...); err == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("tnl %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	// The first start obtains a certificate, which lands in storage.
	first := start()
	cli("zone", "add", "test")
	cli("outpost", "add", "nas")
	cli("route", "add", "app.test", "http://127.0.0.1:1", "--outpost", "nas")
	first.stop()
	if _, err := os.Stat(filepath.Join(stateDir, "caddy", "certificates")); err != nil {
		t.Fatalf("no certificate in storage: %v", err)
	}

	p := start()
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
	seen := map[string]int{}
	for _, l := range strings.Split(p.out.String(), "\n") {
		if i := strings.Index(l, "msg="); i >= 0 {
			seen[l[i:]]++
		}
	}
	for l, n := range seen {
		if n > 1 {
			t.Errorf("%d times: %s", n, l)
		}
	}
	if !strings.Contains(p.out.String(), "started background certificate maintenance") {
		t.Errorf("Caddy's startup line is missing:\n%s", p.out)
	}
	if redirected != 1 {
		t.Errorf("%d \"redirected default logger\" lines, want 1:\n%s", redirected, p.out)
	}
}
