package harness

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

const wantHSTS = "max-age=31536000"

// ownHeaders are what the Edge puts on every reply it makes itself.
var ownHeaders = map[string]string{
	"X-Content-Type-Options":  "nosniff",
	"X-Frame-Options":         "DENY",
	"Referrer-Policy":         "no-referrer",
	"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'",
}

func assertNoCaddy(t *testing.T, label string, h http.Header) {
	t.Helper()
	for k, vs := range h {
		if strings.Contains(strings.ToLower(k+strings.Join(vs, ",")), "caddy") {
			t.Errorf("%s: header %s: %v names Caddy", label, k, vs)
		}
	}
}

func assertOwn(t *testing.T, label string, h http.Header) {
	t.Helper()
	for k, v := range ownHeaders {
		if h.Get(k) != v {
			t.Errorf("%s: %s = %q, want %q", label, k, h.Get(k), v)
		}
	}
	if s := h.Values("Server"); len(s) != 0 {
		t.Errorf("%s: Server = %v, want none", label, s)
	}
	if v := h.Get("Via"); v != "" {
		t.Errorf("%s: Via = %q, want none", label, v)
	}
	assertNoCaddy(t, label, h)
}

func TestEdgeRepliesCarryHardenedHeadersAndNoServer(t *testing.T) {
	h := New(t)
	h.AddOutpost(t, OutpostOptions{Name: "home"})
	ce := h.Edge.StartCaddy(t, CaddyOptions{})
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "ghost.test", Outpost: "no-such-outpost", Service: ClosedAddr(t)}})
	for _, proto := range Protos {
		for _, accept := range []string{"text/html", "", "text/event-stream"} {
			for _, host := range []string{"other.test", "ghost.test"} {
				resp, _ := GetAccepting(t, ce, proto, host, accept)
				assertOwn(t, string(proto)+" "+host+" "+accept, resp.Header)
			}
		}
	}
}

func TestProxiedRepliesHaveHSTSAndNoVia(t *testing.T) {
	h := EdgeWithService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/own" {
			w.Header().Set("Strict-Transport-Security", "max-age=60; includeSubDomains")
			w.Header().Set("Server", "nginx")
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		}
		w.Write([]byte("ok"))
	}))
	for _, proto := range Protos {
		resp, _ := Get(t, h.Edge.Caddy, proto, "app.test", "/plain")
		if got := resp.Header.Get("Strict-Transport-Security"); got != wantHSTS {
			t.Errorf("%s: HSTS = %q, want %q", proto, got, wantHSTS)
		}
		if resp.Header.Get("Via") != "" || len(resp.Header.Values("Server")) != 0 {
			t.Errorf("%s: Via %q, Server %v", proto, resp.Header.Get("Via"), resp.Header.Values("Server"))
		}
		if resp.Header.Get("X-Frame-Options") != "" || resp.Header.Get("Content-Security-Policy") != "" {
			t.Errorf("%s: proxied reply got the Edge's own headers: %v", proto, resp.Header)
		}
		assertNoCaddy(t, string(proto), resp.Header)

		resp, _ = Get(t, h.Edge.Caddy, proto, "app.test", "/own")
		if got := resp.Header.Get("Strict-Transport-Security"); got != "max-age=60; includeSubDomains" {
			t.Errorf("%s: the Service's HSTS = %q", proto, got)
		}
		if resp.Header.Get("Server") != "nginx" || resp.Header.Get("X-Frame-Options") != "SAMEORIGIN" || resp.Header.Get("Via") != "" {
			t.Errorf("%s: Service headers changed: %v", proto, resp.Header)
		}
	}
}

func TestRedirectHasNoHSTSNoServerAndHardenedHeaders(t *testing.T) {
	h := EdgeWithService(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req, _ := http.NewRequestWithContext(Ctx(t), "GET", "http://app.test/x", nil)
	resp, err := h.Edge.Caddy.PlainClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 308 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if resp.Header.Get("Strict-Transport-Security") != "" {
		t.Errorf("redirect sends HSTS")
	}
	assertOwn(t, "redirect", resp.Header)
}

// A connection that drops before the Service's response headers is the Edge's
// own reply, with the hardened headers and no Server header, not Caddy's 502; an
// event stream gets 200 and retry: (ADR 0013).
func TestADroppedConnectionIsAnEdgeReplyNotCaddys502(t *testing.T) {
	h := EdgeWithService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			c.Close()
		}
	}))
	for _, proto := range Protos {
		resp, body := GetAccepting(t, h.Edge.Caddy, proto, "app.test", "text/html")
		if resp.StatusCode != 502 || !strings.Contains(body, "<style>") {
			t.Errorf("%s: got %d %q, want the Edge's 502 page", proto, resp.StatusCode, body)
		}
		assertOwn(t, string(proto)+" 502", resp.Header)

		resp, body = GetAccepting(t, h.Edge.Caddy, proto, "app.test", "text/event-stream")
		if resp.StatusCode != 200 || !strings.Contains(body, "retry: ") {
			t.Errorf("%s: event stream got %d %q, want 200 and retry:", proto, resp.StatusCode, body)
		}
		assertOwn(t, string(proto)+" event stream", resp.Header)
	}
}
