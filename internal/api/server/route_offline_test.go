package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
	"github.com/hyprsh/tnl/internal/edge/liveness"
)

func offlineFixture(t *testing.T, live *liveness.Liveness) *fixture {
	t.Helper()
	f := startConfigured(t, "1.2.3", api.ActorLocal, func(c *server.Config) {
		c.Resolver = edgeResolver()
		c.Liveness = live
	})
	if _, err := addZone(f.client, "example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := addOutpost(f.client, "nas"); err != nil {
		t.Fatal(err)
	}
	return f
}

func addRouteReply(f *fixture) (api.RouteAddReply, error) {
	return api.Call[api.RouteAddReply](context.Background(), f.client, http.MethodPost, api.PathRoutes,
		api.RouteAddRequest{Hostname: "app.example.com", Service: "http://localhost:3000", Outpost: "nas"})
}

func TestRouteAddWarnsWhenTheOutpostIsOffline(t *testing.T) {
	now := time.Now()
	live := liveness.New(liveness.Config{Now: func() time.Time { return now }})
	live.Seed("nas")
	now = now.Add(2 * time.Minute)
	f := offlineFixture(t, live)

	r, err := addRouteReply(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings.Warnings) != 1 || !strings.Contains(r.Warnings.Warnings[0], "Offline") {
		t.Fatalf("warnings %q", r.Warnings.Warnings)
	}
	if len(f.store.Snapshot().Routes) != 1 {
		t.Fatal("the Route is saved either way")
	}
	if !strings.Contains(f.log.String(), "level=warn") {
		t.Fatalf("the Edge did not log the warning:\n%s", f.log)
	}
}

func TestRouteAddDoesNotWarnForAnOnlineOutpost(t *testing.T) {
	live := liveness.New(liveness.Config{})
	live.Seed("nas")
	f := offlineFixture(t, live)
	r, err := addRouteReply(f)
	if err != nil {
		t.Fatal(err)
	}
	if r.Warnings.Warnings != nil {
		t.Fatalf("warnings %q", r.Warnings.Warnings)
	}
}
