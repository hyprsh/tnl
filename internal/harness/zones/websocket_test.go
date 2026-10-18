package zones

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"

	. "github.com/hyprsh/tnl/internal/harness"
)

// upgradeHandler switches to a bare protocol that is enough of a WebSocket for
// Caddy's proxy: it answers 101 and then sends a line every 20 ms until the
// peer goes away.
func upgradeHandler(w http.ResponseWriter, r *http.Request) {
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	for {
		if _, err := fmt.Fprint(rw, "x\n"); err != nil || rw.Flush() != nil {
			return
		}
		time.Sleep(20 * time.Millisecond) // paces the stream
	}
}

// openWebSocket upgrades a connection through the Edge's HTTPS port and returns
// a func that reads one line from it, false once the connection is closed.
func openWebSocket(t *testing.T, re *RealEdge, host string) (next func() bool) {
	t.Helper()
	conn, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(re.Caddy.HTTPSPort)),
		&tls.Config{ServerName: host, RootCAs: re.Caddy.Roots, NextProtos: []string{"http/1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", host)
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("the upgrade answered %q, %v", status, err)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	return func() bool {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := br.ReadString('\n')
		return err == nil && line != ""
	}
}

func wildcardEdgeWithWebSocket(t *testing.T) (re *RealEdge, fake *FakeDNS) {
	t.Helper()
	svc := NewHTTPService(t, http.HandlerFunc(upgradeHandler))
	fake = StartFakeDNS(t, "example.test", dnsToken)
	re = StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "other.test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "ws.other.test", "http://"+svc.Addr(), "nas")
	return re, fake
}

// Replacing a wildcard Zone's token reloads nothing, so an open WebSocket stays
// open.
func TestAWebSocketStaysOpenWhenZoneEditReplacesTheToken(t *testing.T) {
	re, fake := wildcardEdgeWithWebSocket(t)
	if _, err := zoneAddWildcard(t, re, "example.test", "fake", dnsToken); err != nil {
		t.Fatal(err)
	}
	next := openWebSocket(t, re, "ws.other.test")
	if !next() {
		t.Fatal("the WebSocket did not start")
	}
	fake.SetToken("n3w-T0KEN-0123456789")
	if _, err := zoneEdit(t, re, api.ZoneEditRequest{Name: "example.test", Token: "n3w-T0KEN-0123456789"}); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if !next() {
			t.Fatal("the WebSocket closed")
		}
	}
}

// A Caddy reload, which a wildcard Zone's add makes, closes WebSockets: the
// spec's one exception to a reload keeping every stream.
func TestAWebSocketClosesWhenCaddyReloads(t *testing.T) {
	re, _ := wildcardEdgeWithWebSocket(t)
	next := openWebSocket(t, re, "ws.other.test")
	if !next() {
		t.Fatal("the WebSocket did not start")
	}
	if _, err := zoneAddWildcard(t, re, "example.test", "fake", dnsToken); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for next() {
		if time.Now().After(deadline) {
			t.Fatal("the WebSocket survived the reload")
		}
	}
}
