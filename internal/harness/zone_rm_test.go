package harness

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/edge/caddyx"
)

func zoneRm(t *testing.T, re *RealEdge, name string, with bool) (api.ZoneRmReply, error) {
	t.Helper()
	return api.Call[api.ZoneRmReply](ctx(t), re.API, http.MethodPost, api.PathZoneRm, api.ZoneRmRequest{Name: name, WithRoutes: with})
}

func hello() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "hello") })
}

// With Zones example.test and lab.example.test, a Hostname under the nested one
// belongs to it. Removing it moves the Route to example.test: it keeps serving
// with the certificate it has, no new order at the CA and no Caddy reload.
func TestZoneRmMovesARouteToTheParentWithoutATLSError(t *testing.T) {
	svc := NewHTTPService(t, hello())
	pb := StartPebble(t, PebbleOptions{})
	re := StartEdgeWith(t, EdgeOptions{Pebble: pb})
	re.AddZone(t, "example.test")
	re.AddZone(t, "lab.example.test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.lab.example.test", "http://"+svc.Addr(), "nas")
	if resp, body := get(t, re.Caddy, H1, "app.lab.example.test", "/"); resp.StatusCode != 200 || body != "hello" {
		t.Fatalf("before: %d %q", resp.StatusCode, body)
	}
	orders, loads := pb.Orders(), caddyx.Loads()

	r, err := zoneRm(t, re, "lab.example.test", false)
	if err != nil || len(r.Moved) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	for _, proto := range Protos {
		if resp, body := get(t, re.Caddy, proto, "app.lab.example.test", "/"); resp.StatusCode != 200 || body != "hello" {
			t.Fatalf("%s after zone rm: %d %q", proto, resp.StatusCode, body)
		}
	}
	if n := pb.Orders(); n != orders {
		t.Errorf("zone rm made %d more orders", n-orders)
	}
	if n := caddyx.Loads(); n != loads {
		t.Errorf("zone rm reloaded Caddy")
	}
	if re.Certs.Managing() != 1 {
		t.Errorf("the moved Route's Hostname is no longer managed: %d", re.Certs.Managing())
	}
}

// A Zone that alone holds Routes is not removed: the refusal lists them and
// nothing changes. With --with-routes they go, their open streams close and
// their certificates stop renewing.
func TestZoneRmWithRoutesClosesStreamsAndStopsTheCertificates(t *testing.T) {
	stream := NewHTTPService(t, http.HandlerFunc(sseHandler))
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "example.test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "s.example.test", "http://"+stream.Addr(), "nas")
	re.AddRoute(t, "t.example.test", "http://"+stream.Addr(), "nas")
	next := openStream(t, re, "s.example.test")
	if !next() {
		t.Fatal("stream did not start")
	}
	if re.Certs.Managing() != 2 {
		t.Fatalf("managing %d", re.Certs.Managing())
	}

	_, err := zoneRm(t, re, "example.test", false)
	var e *api.Error
	if !errors.As(err, &e) || e.Status != http.StatusConflict || !strings.Contains(e.Message, "s.example.test") || !strings.Contains(e.Message, "t.example.test") {
		t.Fatalf("refusal: %v", err)
	}
	if len(re.Store.Snapshot().Zones) != 1 || len(re.Store.Snapshot().Routes) != 2 || re.Certs.Managing() != 2 {
		t.Fatal("the refusal changed something")
	}
	if !next() {
		t.Fatal("the refusal closed a stream")
	}

	if _, err := zoneRm(t, re, "example.test", true); err != nil {
		t.Fatal(err)
	}
	ended := false
	for i := 0; i < 1000 && !ended; i++ {
		ended = !next()
	}
	if !ended {
		t.Fatal("--with-routes did not close the stream")
	}
	if n := re.Certs.Managing(); n != 0 {
		t.Errorf("%d Hostnames still managed after the Zone and its Routes went", n)
	}
	if len(re.Store.Snapshot().Routes) != 0 {
		t.Error("Routes left")
	}
}

// route rm stops the removed Hostname's certificate too: #46 left it managed.
func TestRouteRmStopsManagingTheHostname(t *testing.T) {
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "example.test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "a.example.test", "http://127.0.0.1:1", "nas")
	re.AddRoute(t, "b.example.test", "http://127.0.0.1:1", "nas")
	if _, err := api.Call[api.RouteInfo](ctx(t), re.API, http.MethodPost, api.PathRouteRm, api.RouteRmRequest{Hostname: "a.example.test"}); err != nil {
		t.Fatal(err)
	}
	if re.Certs.Managing() != 1 {
		t.Errorf("managing %d, want only b.example.test", re.Certs.Managing())
	}
	if _, ok := re.Certs.Status("a.example.test"); ok {
		t.Error("the removed Hostname still has a status")
	}
}
