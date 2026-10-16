package server_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
)

func rmRoute(c *api.Client, host string) (api.RouteRmReply, error) {
	return api.Call[api.RouteRmReply](context.Background(), c, http.MethodPost, api.PathRouteRm, api.RouteRmRequest{Hostname: host})
}

func TestRouteRmRemovesOnlyThatRouteAndLogsItAsLocal(t *testing.T) {
	f := routeFixture(t)
	addRoute(f.client, "a.example.com", "http://a:1", "nas")
	addRoute(f.client, "b.example.com", "http://b:1", "nas")
	r, err := rmRoute(f.client, "A.Example.com")
	if err != nil || r.Hostname != "a.example.com" {
		t.Fatalf("%+v %v", r, err)
	}
	got := f.store.Snapshot().Routes
	if len(got) != 1 || got[0].Hostname != "b.example.com" {
		t.Fatalf("store has %+v", got)
	}
	var line string
	for _, l := range strings.Split(f.log.String(), "\n") {
		if strings.Contains(l, "route removed") {
			line = l
		}
	}
	for _, want := range []string{"level=info", "actor=local", "a.example.com"} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line %q lacks %q", line, want)
		}
	}
}

func TestRouteRmOfAnUnknownHostnameIsA404(t *testing.T) {
	f := routeFixture(t)
	_, err := rmRoute(f.client, "ghost.example.com")
	var e *api.Error
	if !errors.As(err, &e) || e.Status != http.StatusNotFound {
		t.Fatalf("err=%v", err)
	}
}

func TestAnActorThatIsNeitherLocalNorAnOutpostCannotRemoveRoutes(t *testing.T) {
	f := start(t, "1.2.3", api.Actor("stranger"))
	_, err := rmRoute(f.client, "app.example.com")
	var e *api.Error
	if !errors.As(err, &e) || e.Status != http.StatusForbidden {
		t.Fatalf("err=%v", err)
	}
}
