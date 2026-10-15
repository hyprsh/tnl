package edge

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	"github.com/hyprsh/tnl/internal/logx"
)

// freeBothPort finds a port free on TCP and on UDP.
func freeBothPort(t *testing.T) int {
	t.Helper()
	for range 50 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		if pc, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			pc.Close()
			return port
		}
	}
	t.Fatal("no free port")
	return 0
}

// startWithWeb starts an Edge with Caddy on a free HTTPS port, WireGuard on
// wgPort (0 asks startWithWeb for the HTTPS port), and returns the log and the
// protocols the https server speaks.
func startWithWeb(t *testing.T, wgOnHTTPSPort bool) (*syncBuf, []string) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", "")
	httpsPort, httpPort := freeBothPort(t), freeBothPort(t)
	wg := 0
	if wgOnHTTPSPort {
		wg = httpsPort
	}
	buf := &syncBuf{}
	lg := logx.New(logx.Options{Level: slog.LevelInfo, Out: buf})
	ctx, cancel := context.WithCancel(context.Background())
	up := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Address: "tnl.example.com", StateDir: filepath.Join(shortDir(t), "state"), Log: lg, WireGuardPort: wg,
			Web: &Web{
				HTTPListen: fmt.Sprintf("127.0.0.1:%d", httpPort), HTTPSListen: fmt.Sprintf("127.0.0.1:%d", httpsPort),
				HTTPPort: httpPort, HTTPSPort: httpsPort,
			},
			Started: func(*Running) { close(up) },
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-up:
	case err := <-done:
		t.Fatalf("Run: %v\n%s", err, buf.String())
	case <-time.After(30 * time.Second):
		t.Fatal("the Edge did not start")
	}
	app, err := caddy.ActiveContext().App("http")
	if err != nil {
		t.Fatal(err)
	}
	return buf, app.(*caddyhttp.App).Servers["https"].Protocols
}

func TestHTTP3IsOnWhenWireGuardHasItsOwnPort(t *testing.T) {
	buf, protos := startWithWeb(t, false)
	if fmt.Sprint(protos) != "[h1 h2 h3]" {
		t.Errorf("protocols = %v, want h1 h2 h3\n%s", protos, buf.String())
	}
}

func TestWireGuardOnTheHTTPSPortSwitchesHTTP3OffWithALogLine(t *testing.T) {
	buf, protos := startWithWeb(t, true)
	if fmt.Sprint(protos) != "[h1 h2]" {
		t.Errorf("protocols = %v, want h1 h2\n%s", protos, buf.String())
	}
	if !strings.Contains(buf.String(), "HTTP/3 is off") {
		t.Errorf("no log line saying HTTP/3 is off:\n%s", buf.String())
	}
}
