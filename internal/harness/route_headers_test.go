package harness

import (
	"net/http"
	"testing"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

func TestARouteWithNoHSTSSendsNoneAndExtraHeadersGoOnProxiedRepliesOnly(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/own" {
			w.Header().Set("Permissions-Policy", "camera=(self)")
		}
		w.Write([]byte("ok"))
	}))
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home"})
	ce := h.Edge.StartCaddy(t, CaddyOptions{})
	extra := []routes.Header{
		{Name: "Content-Security-Policy", Value: "default-src 'self'"},
		{Name: "Permissions-Policy", Value: "camera=()"},
	}
	h.Edge.Routes.Swap([]routes.Route{
		{Hostname: "plain.test", Outpost: o.Name, Service: svc.Addr(), NoHSTS: true},
		{Hostname: "extra.test", Outpost: o.Name, Service: svc.Addr(), Headers: extra},
		{Hostname: "dead.test", Outpost: o.Name, Service: closedAddr(t), NoHSTS: true, Headers: extra},
		{Hostname: "deadhsts.test", Outpost: o.Name, Service: closedAddr(t), Headers: extra},
	})
	// A dead Service costs the Edge's whole dial retry budget (5 s) before its 502,
	// so each protocol's checks run as subtests side by side.
	for _, proto := range Protos {
		t.Run(string(proto)+"/proxied", func(t *testing.T) {
			t.Parallel()
			resp, _ := get(t, ce, proto, "plain.test", "/")
			if v := resp.Header.Values("Strict-Transport-Security"); len(v) != 0 {
				t.Errorf("%s: --no-hsts Route sent HSTS %v", proto, v)
			}
			resp, _ = get(t, ce, proto, "extra.test", "/")
			if got := resp.Header.Get("Content-Security-Policy"); got != "default-src 'self'" {
				t.Errorf("%s: extra CSP = %q", proto, got)
			}
			if got := resp.Header.Get("Strict-Transport-Security"); got != wantHSTS {
				t.Errorf("%s: HSTS = %q, want %q", proto, got, wantHSTS)
			}
			if got := resp.Header.Get("Permissions-Policy"); got != "camera=()" {
				t.Errorf("%s: extra Permissions-Policy = %q", proto, got)
			}
			resp, _ = get(t, ce, proto, "extra.test", "/own")
			if got := resp.Header.Values("Permissions-Policy"); len(got) != 1 || got[0] != "camera=(self)" {
				t.Errorf("%s: the Service's own header lost: %v", proto, got)
			}
		})
		// The Edge's own replies keep their fixed headers and get no extra ones.
		t.Run(string(proto)+"/502 of a --no-hsts Route", func(t *testing.T) {
			t.Parallel()
			resp, _ := getAccepting(t, ce, proto, "dead.test", "text/html")
			assertOwn(t, string(proto)+" 502", resp.Header)
			if resp.Header.Get("Permissions-Policy") != "" || len(resp.Header.Values("Strict-Transport-Security")) != 0 {
				t.Errorf("%s: 502 of a --no-hsts Route: %v", proto, resp.Header)
			}
		})
		t.Run(string(proto)+"/502 of a Route with HSTS", func(t *testing.T) {
			t.Parallel()
			resp, _ := getAccepting(t, ce, proto, "deadhsts.test", "text/html")
			if resp.Header.Get("Strict-Transport-Security") != wantHSTS || resp.Header.Get("Permissions-Policy") != "" {
				t.Errorf("%s: 502 headers: %v", proto, resp.Header)
			}
		})
	}
}

func TestADeclaredRouteCanSetNoHSTSAndExtraHeaders(t *testing.T) {
	svc := NewHTTPService(t, echoHost())
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	tok := re.AddOutpost(t, "nas")
	line := DeclaredLine("A", "a.test", "http://"+svc.Addr())
	line.NoHSTS = true
	line.Headers = []string{"Content-Security-Policy: default-src 'self'; img-src data:"}
	re.ConnectWith(t, "nas", tok, ConnectOptions{Declaration: []outpostLine{line}})
	waitServed(t, re, "a.test")
	for _, proto := range Protos {
		resp, _ := get(t, re.Caddy, proto, "a.test", "/")
		if v := resp.Header.Values("Strict-Transport-Security"); len(v) != 0 {
			t.Errorf("%s: HSTS %v", proto, v)
		}
		if got := resp.Header.Get("Content-Security-Policy"); got != "default-src 'self'; img-src data:" {
			t.Errorf("%s: CSP = %q", proto, got)
		}
	}
}
