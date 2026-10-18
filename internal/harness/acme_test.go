package harness

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

func TestFirstRequestToARoutedHostnameGetsAPebbleCertificate(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hi") }))
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home"})
	pb := StartPebble(t, PebbleOptions{})
	ce := h.Edge.StartCaddy(t, CaddyOptions{Pebble: pb})
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "app.test", Outpost: o.Name, Service: svc.Addr()}})

	for _, proto := range Protos {
		resp, body := Get(t, ce, proto, "app.test", "/")
		if resp.StatusCode != 200 || body != "hi" {
			t.Fatalf("%s: got %d %q", proto, resp.StatusCode, body)
		}
		leaf := resp.TLS.PeerCertificates[0]
		if err := leaf.VerifyHostname("app.test"); err != nil {
			t.Errorf("%s: %v", proto, err)
		}
		if _, err := leaf.Verify(verifyOpts(pb, resp.TLS.PeerCertificates[1:])); err != nil {
			t.Errorf("%s: certificate is not Pebble's: %v", proto, err)
		}
	}
	if n := pb.Orders(); n != 1 {
		t.Errorf("Pebble saw %d orders, want 1 (the certificate is cached)", n)
	}
}

func TestSNIWithoutARouteTriggersNoIssuance(t *testing.T) {
	h := New(t)
	pb := StartPebble(t, PebbleOptions{})
	ce := h.Edge.StartCaddy(t, CaddyOptions{Pebble: pb})
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "app.test", Outpost: "home", Service: "s:1"}})

	c, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp",
		fmt.Sprintf("127.0.0.1:%d", ce.HTTPSPort), &tls.Config{ServerName: "nope.test", RootCAs: ce.Roots})
	if err == nil {
		c.Close()
		t.Fatal("handshake for an unrouted Hostname succeeded")
	}
	if n := pb.Orders(); n != 0 {
		t.Errorf("Pebble saw %d orders for an unrouted Hostname", n)
	}
}

func TestPort80AnswersHTTP01AndStillRedirects(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hi") }))
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home"})
	pb := StartPebble(t, PebbleOptions{Validate: true})
	ce := h.Edge.StartCaddy(t, CaddyOptions{Pebble: pb})
	// Pebble validates by resolving the Hostname, so use one that resolves.
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "localhost", Outpost: o.Name, Service: svc.Addr()}})

	// Issuance only succeeds if port 80 answered Pebble's HTTP-01 request.
	if resp, body := Get(t, ce, H1, "localhost", "/"); resp.StatusCode != 200 || body != "hi" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}

	// (Caddy tries TLS-ALPN-01 first, which Pebble can't reach here, and then
	// falls back to HTTP-01, so more than one order is normal.)

	req, _ := http.NewRequestWithContext(Ctx(t), "GET", "http://localhost/some/path?a=1", nil)
	resp, err := ce.PlainClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	want := fmt.Sprintf("https://localhost:%d/some/path?a=1", ce.HTTPSPort)
	if resp.StatusCode != 308 || resp.Header.Get("Location") != want {
		t.Errorf("got %d %q, want 308 to %s", resp.StatusCode, resp.Header.Get("Location"), want)
	}
}
