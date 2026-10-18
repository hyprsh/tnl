package harness

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
)

func ptr[T any](v T) *T { return &v }

func routeEdit(t *testing.T, re *RealEdge, req api.RouteEditRequest) error {
	t.Helper()
	_, err := api.Call[api.RouteInfo](Ctx(t), re.API, http.MethodPost, api.PathRouteEdit, req)
	return err
}

func TestHTTPSServiceIsServedVerifiedAndRefusedWhenItsCertificateDoesNotVerify(t *testing.T) {
	trusted := NewHTTPSService(t, EchoHost())
	untrusted := NewHTTPSService(t, EchoHost())
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), ServiceRoots: trusted.CA})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "ok.test", "https://"+trusted.Addr(), "nas")
	re.AddRoute(t, "bad.test", "https://"+untrusted.Addr(), "nas")

	if resp, body := Get(t, re.Caddy, H1, "ok.test", "/"); resp.StatusCode != 200 || body != "ok.test" {
		t.Fatalf("trusted https Service: %d %q", resp.StatusCode, body)
	}
	if resp, _ := Get(t, re.Caddy, H1, "bad.test", "/"); resp.StatusCode != 502 {
		t.Fatalf("unverifiable certificate: got %d, want 502", resp.StatusCode)
	}
	if err := routeEdit(t, re, api.RouteEditRequest{Hostname: "bad.test", InsecureSkipVerify: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if resp, _ := Get(t, re.Caddy, H1, "bad.test", "/"); resp.StatusCode != 200 {
		t.Fatalf("with --insecure-skip-verify: got %d, want 200", resp.StatusCode)
	}
	if err := routeEdit(t, re, api.RouteEditRequest{Hostname: "bad.test", InsecureSkipVerify: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if resp, _ := Get(t, re.Caddy, H1, "bad.test", "/"); resp.StatusCode != 502 {
		t.Fatalf("after turning verification back on: got %d, want 502", resp.StatusCode)
	}
}

func TestHostHeaderOverridesTheVisitorsHostUntilCleared(t *testing.T) {
	svc := NewHTTPService(t, EchoHost())
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	_, err := api.Call[api.RouteInfo](Ctx(t), re.API, http.MethodPost, api.PathRoutes,
		api.RouteAddRequest{Hostname: "a.test", Service: "http://" + svc.Addr(), Outpost: "nas", HostHeader: "internal.lan"})
	if err != nil {
		t.Fatal(err)
	}
	if _, body := Get(t, re.Caddy, H1, "a.test", "/"); body != "internal.lan" {
		t.Fatalf("the Service saw Host %q, want internal.lan", body)
	}
	if err := routeEdit(t, re, api.RouteEditRequest{Hostname: "a.test", HostHeader: ptr("other.lan")}); err != nil {
		t.Fatal(err)
	}
	if _, body := Get(t, re.Caddy, H1, "a.test", "/"); body != "other.lan" {
		t.Fatalf("after edit the Service saw Host %q", body)
	}
	if err := routeEdit(t, re, api.RouteEditRequest{Hostname: "a.test", HostHeader: ptr("")}); err != nil {
		t.Fatal(err)
	}
	if _, body := Get(t, re.Caddy, H1, "a.test", "/"); body != "a.test" {
		t.Fatalf("after clearing the Service saw Host %q, want the Visitor's", body)
	}
	list, err := api.Call[api.RouteList](Ctx(t), re.API, http.MethodGet, api.PathRoutes, nil)
	if err != nil || len(list.Routes[0].Options) != 0 {
		t.Fatalf("options after clearing: %v %v", list, err)
	}
}

func TestRouteEditChangesServiceAndOutpostAndClosesOnlyItsOwnStream(t *testing.T) {
	svcA := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "A") }))
	svcB := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "B") }))
	stream := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sse" {
			fmt.Fprint(w, "ok")
			return
		}
		SSEHandler(w, r)
	}))
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.JoinOutpost(t, "pi")
	re.AddRoute(t, "a.test", "http://"+svcA.Addr(), "nas")
	re.AddRoute(t, "s.test", "http://"+stream.Addr(), "nas")
	Get(t, re.Caddy, H1, "a.test", "/")
	Get(t, re.Caddy, H1, "s.test", "/")
	nextS := OpenStream(t, re, "s.test")
	if !nextS() {
		t.Fatal("stream did not start")
	}

	if err := routeEdit(t, re, api.RouteEditRequest{Hostname: "a.test", Service: ptr("http://" + svcB.Addr())}); err != nil {
		t.Fatal(err)
	}
	if _, body := Get(t, re.Caddy, H1, "a.test", "/"); body != "B" {
		t.Fatalf("after changing the Service: %q", body)
	}
	for range 2 {
		if !nextS() {
			t.Fatal("editing a.test touched s.test's stream")
		}
	}

	// Each edit closes that Route's open stream, even an option alone.
	for _, edit := range []api.RouteEditRequest{
		{Hostname: "s.test", HostHeader: ptr("x.lan")},
		{Hostname: "s.test", InsecureSkipVerify: ptr(true)},
		{Hostname: "s.test", Outpost: ptr("pi")},
		{Hostname: "s.test", Service: ptr("http://" + stream.Addr())},
	} {
		// The first edit's stream is the one opened above; later ones open anew.
		next := nextS
		if edit.HostHeader == nil {
			next = OpenStream(t, re, "s.test")
		}
		if !next() {
			t.Fatal("stream did not start")
		}
		if err := routeEdit(t, re, edit); err != nil {
			t.Fatal(err)
		}
		ended := false
		for i := 0; i < 1000 && !ended; i++ {
			ended = !next()
		}
		if !ended {
			t.Fatalf("edit %+v did not close the stream", edit)
		}
	}
	list, err := api.Call[api.RouteList](Ctx(t), re.API, http.MethodGet, api.PathRoutes, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list.Routes {
		if r.Hostname == "s.test" {
			if r.Outpost != "pi" || !slices.Equal(r.Options, []string{"host-header=x.lan", "insecure-skip-verify=true"}) {
				t.Fatalf("s.test is %+v", r)
			}
		}
	}
}

func TestRouteEditRefusals(t *testing.T) {
	re := StartEdge(t)
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "a.test", "http://127.0.0.1:1", "nas")
	for name, tc := range map[string]struct {
		req    api.RouteEditRequest
		status int
	}{
		"unknown Hostname":  {api.RouteEditRequest{Hostname: "z.test", HostHeader: ptr("x")}, 404},
		"unknown Outpost":   {api.RouteEditRequest{Hostname: "a.test", Outpost: ptr("nope")}, 400},
		"bad Service":       {api.RouteEditRequest{Hostname: "a.test", Service: ptr("ftp://x")}, 400},
		"nothing to change": {api.RouteEditRequest{Hostname: "a.test"}, 400},
	} {
		err := routeEdit(t, re, tc.req)
		if e, ok := err.(*api.Error); !ok || e.Status != tc.status {
			t.Errorf("%s: %v, want status %d", name, err, tc.status)
		}
	}
}
