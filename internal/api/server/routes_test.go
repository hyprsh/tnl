package server_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
)

func addRoute(c *api.Client, host, service, outpost string) (api.RouteInfo, error) {
	return api.Call[api.RouteInfo](context.Background(), c, http.MethodPost, api.PathRoutes,
		api.RouteAddRequest{Hostname: host, Service: service, Outpost: outpost})
}

// routeFixture has the Zone example.com and the Outposts nas and laptop.
func routeFixture(t *testing.T) *fixture {
	t.Helper()
	f := start(t, "1.2.3", api.ActorLocal)
	if _, err := addZone(f.client, "example.com"); err != nil {
		t.Fatal(err)
	}
	for _, o := range []string{"nas", "laptop"} {
		if _, err := addOutpost(f.client, o); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestRouteAddSavesTheRouteAndLogsItAsLocal(t *testing.T) {
	f := routeFixture(t)
	r, err := addRoute(f.client, "App.Example.com", "http://localhost:3000", "nas")
	if err != nil {
		t.Fatal(err)
	}
	if r.Hostname != "app.example.com" || r.Outpost != "nas" || r.Service != "http://localhost:3000" {
		t.Fatalf("%+v", r)
	}
	got := f.store.Snapshot().Routes
	if len(got) != 1 || got[0].Hostname != "app.example.com" || got[0].Service != "http://localhost:3000" {
		t.Fatalf("store has %+v", got)
	}
	var line string
	for _, l := range strings.Split(f.log.String(), "\n") {
		if strings.Contains(l, "route added") {
			line = l
		}
	}
	for _, want := range []string{"level=info", "actor=local", "app.example.com", "nas"} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line %q lacks %q", line, want)
		}
	}
}

func TestRouteAddRefusals(t *testing.T) {
	f := routeFixture(t)
	if _, err := addRoute(f.client, "app.example.com", "http://localhost:3000", "nas"); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ host, service, outpost, want string }{
		"taken, naming the holder": {"app.example.com", "http://x:1", "laptop", "nas"},
		"outside every Zone":       {"app.other.org", "http://x:1", "nas", "Zone"},
		"wildcard":                 {"*.example.com", "http://x:1", "nas", "wildcard"},
		"unknown Outpost":          {"b.example.com", "http://x:1", "ghost", "ghost"},
		"no Outpost":               {"b.example.com", "http://x:1", "", "Outpost"},
		"path":                     {"b.example.com", "http://x:1/api", "nas", "path"},
		"https":                    {"b.example.com", "https://x:1", "nas", "http"},
		"other scheme":             {"b.example.com", "ftp://x:1", "nas", "http"},
		"no host":                  {"b.example.com", "http://:80", "nas", "host"},
		"bad port":                 {"b.example.com", "http://x:99999", "nas", "port"},
		"not a URL":                {"b.example.com", "localhost:3000", "nas", "http"},
		"bad Hostname":             {"b_.example.com", "http://x:1", "nas", "invalid"},
	} {
		_, err := addRoute(f.client, tc.host, tc.service, tc.outpost)
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Status < 400 || !strings.Contains(ae.Message, tc.want) {
			t.Errorf("%s: err=%v, want it to contain %q", name, err, tc.want)
		}
	}
	if n := len(f.store.Snapshot().Routes); n != 1 {
		t.Fatalf("%d routes", n)
	}
}

func TestTakenHostnameIsA409(t *testing.T) {
	f := routeFixture(t)
	addRoute(f.client, "app.example.com", "http://x:1", "nas")
	_, err := addRoute(f.client, "app.example.com", "http://x:1", "laptop")
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusConflict {
		t.Fatalf("err=%v", err)
	}
}

func TestOtherActorsCannotAddRoutes(t *testing.T) {
	f := start(t, "1.2.3", api.OutpostActor("nas"))
	if _, err := addRoute(f.client, "app.example.com", "http://x:1", "nas"); err == nil {
		t.Fatal("outpost added a route")
	}
}

func TestRouteLsIsSortedByHostname(t *testing.T) {
	f := routeFixture(t)
	addRoute(f.client, "b.example.com", "http://nas", "nas")
	addRoute(f.client, "a.example.com", "http://laptop:8080", "laptop")
	list, err := api.Call[api.RouteList](context.Background(), f.client, http.MethodGet, api.PathRoutes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Routes) != 2 || list.Routes[0].Hostname != "a.example.com" || list.Routes[1].Service != "http://nas" {
		t.Fatalf("%+v", list)
	}
}
