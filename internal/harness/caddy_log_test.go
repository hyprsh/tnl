package harness

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/hyprsh/tnl/internal/edge/routes"
	"github.com/hyprsh/tnl/internal/logx"
)

// loudLines are the lines at info or above.
func loudLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		// Startup housekeeping, which runs in background goroutines and so can
		// log after the test has counted: it is not a handshake.
		if strings.Contains(ln, "cleaning storage") || strings.Contains(ln, "started background certificate maintenance") {
			continue
		}
		for _, l := range []string{" level=info ", " level=warn ", " level=error "} {
			if strings.Contains(ln, l) {
				out = append(out, ln)
			}
		}
	}
	return out
}

func TestFailedHTTP3HandshakeAndUnknownSNIStayBelowInfo(t *testing.T) {
	buf := &LockedBuf{}
	lg := logx.New(logx.Options{Level: slog.LevelDebug, Out: buf})
	h := New(t)
	pb := StartPebble(t, PebbleOptions{})
	ce := h.Edge.StartCaddy(t, CaddyOptions{Pebble: pb, Log: lg})
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "app.test", Outpost: "home", Service: "s:1"}})

	// Caddy's entries arrive as tnl lines with a logger= field.
	if !strings.Contains(buf.String(), "logger=") {
		t.Fatalf("no Caddy line reached the logger:\n%s", buf.String())
	}
	before := len(loudLines(buf.String()))

	addr := fmt.Sprintf("127.0.0.1:%d", ce.HTTPSPort)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// A QUIC handshake that fails: no shared ALPN.
	if c, err := quic.DialAddr(ctx, addr, &tls.Config{ServerName: "app.test", RootCAs: ce.Roots, NextProtos: []string{"not-h3"}}, nil); err == nil {
		c.CloseWithError(0, "")
		t.Fatal("handshake with no shared ALPN succeeded")
	}
	// A QUIC handshake for a Hostname without a Route.
	if c, err := quic.DialAddr(ctx, addr, &tls.Config{ServerName: "nope.test", RootCAs: ce.Roots, NextProtos: []string{"h3"}}, nil); err == nil {
		c.CloseWithError(0, "")
		t.Fatal("HTTP/3 handshake for an unrouted Hostname succeeded")
	}
	// A TLS handshake with an unknown SNI.
	if c, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr,
		&tls.Config{ServerName: "nope.test", RootCAs: ce.Roots}); err == nil {
		c.Close()
		t.Fatal("handshake for an unrouted Hostname succeeded")
	}
	time.Sleep(300 * time.Millisecond) // the server logs after the client has failed

	if loud := loudLines(buf.String()); len(loud) != before {
		t.Errorf("failed handshakes logged at info or above:\n%s", strings.Join(loud[before:], "\n"))
	}
}

func TestGoLogPackageComesOutThroughTnlsLogger(t *testing.T) {
	buf := &LockedBuf{}
	lg := logx.New(logx.Options{Level: slog.LevelDebug, Out: buf})
	old := slog.Default()
	slog.SetDefault(lg)
	t.Cleanup(func() { slog.SetDefault(old) })

	h := New(t)
	h.Edge.StartCaddy(t, CaddyOptions{Log: lg})
	log.Print("stray line from a library")
	if !strings.Contains(buf.String(), "stray line from a library") {
		t.Fatalf("Go's log line did not reach tnl's logger:\n%s", buf.String())
	}
}
