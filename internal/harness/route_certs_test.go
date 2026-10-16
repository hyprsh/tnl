package harness

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

// handshake does a TLS handshake with the Edge's HTTPS port for the Hostname
// and returns the certificate it was given.
func handshake(t *testing.T, ce *CaddyEdge, host string) (*tls.ConnectionState, error) {
	t.Helper()
	c, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp",
		fmt.Sprintf("127.0.0.1:%d", ce.HTTPSPort), &tls.Config{ServerName: host, RootCAs: ce.Roots})
	if err != nil {
		return nil, err
	}
	defer c.Close()
	st := c.ConnectionState()
	return &st, nil
}

// route add returns once the certificate is issued, so the first handshake for
// the Hostname asks the CA for nothing more.
func TestRouteAddReturnsOnceTheCertificateIsIssued(t *testing.T) {
	pb := StartPebble(t, PebbleOptions{})
	e := StartEdgeWith(t, EdgeOptions{Pebble: pb})
	e.AddZone(t, "example.test")
	e.AddOutpost(t, "home")

	r := e.AddRouteReply(t, "app.example.test", "http://localhost:3000", "home")
	if ws := withoutNoTestDial(r.Warnings.Warnings); ws != nil {
		t.Fatalf("warnings %q", ws)
	}
	st, ok := e.Certs.Status("app.example.test")
	if !ok || st.NotAfter.IsZero() || st.Obtained != 1 {
		t.Fatalf("status after route add: %+v", st)
	}
	orders := pb.Orders()
	if orders == 0 {
		t.Fatal("route add returned without any order at the CA")
	}

	cs, err := handshake(t, e.Caddy, "app.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PeerCertificates[0].Verify(verifyOpts(pb, cs.PeerCertificates[1:])); err != nil {
		t.Errorf("the certificate is not Pebble's: %v", err)
	}
	if n := pb.Orders(); n != orders {
		t.Errorf("the handshake made %d more orders, want none (no on-demand issuance)", n-orders)
	}
}

// When the CA refuses, route add waits, warns and still succeeds; Caddy keeps
// the Hostname, and the next load hands it over again and obtains it.
func TestRouteAddWarnsWhenTheCARefusesAndALaterRetryObtainsIt(t *testing.T) {
	pb := StartPebble(t, PebbleOptions{})
	pb.Refuse(true)
	log := new(lockedBuf)
	opts := EdgeOptions{Pebble: pb, CertWait: 3 * time.Second, LogOut: log, LogLevel: slog.LevelWarn}
	e := StartEdgeWith(t, opts)
	e.AddZone(t, "example.test")
	e.AddOutpost(t, "home")

	start := time.Now()
	r := e.AddRouteReply(t, "app.example.test", "http://localhost:3000", "home")
	if time.Since(start) < 2*time.Second {
		t.Errorf("route add returned after %v: it did not wait", time.Since(start))
	}
	if ws := withoutNoTestDial(r.Warnings.Warnings); len(ws) != 1 || !strings.Contains(ws[0], "keeps retrying") {
		t.Fatalf("warnings %q", r.Warnings.Warnings)
	}
	if len(e.Store.Snapshot().Routes) != 1 {
		t.Fatal("the Route was not saved")
	}
	// The Edge logs the first failed attempt at warn, and the CLI's warning too.
	waitFor(t, "the failure line", 10*time.Second, func() bool {
		return strings.Contains(log.String(), `msg="certificate attempt failed"`) && strings.Contains(log.String(), "level=warn")
	})

	// The CA recovers; a restart hands every Route Hostname over again.
	pb.Refuse(false)
	dir := e.StateDir
	e.Stop()
	opts.StateDir = dir
	e2 := StartEdgeWith(t, opts)
	waitFor(t, "the certificate after the retry", 30*time.Second, func() bool {
		st, _ := e2.Certs.Status("app.example.test")
		return st.Obtained == 1 && !st.NotAfter.IsZero()
	})
}

// withoutNoTestDial drops the warning that an Outpost which never joined was
// not test-dialed, for tests about the certificate's warnings.
func withoutNoTestDial(ws []string) []string {
	var out []string
	for _, w := range ws {
		if !strings.Contains(w, "has not connected yet") {
			out = append(out, w)
		}
	}
	return out
}
