package server_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
)

type closedRoutes struct {
	mu    sync.Mutex
	hosts []string
}

func (c *closedRoutes) CloseRoute(h string) { c.mu.Lock(); c.hosts = append(c.hosts, h); c.mu.Unlock() }
func (c *closedRoutes) CloseOutpost(string) {}

func rmZone(c *api.Client, name string, with bool) (api.ZoneRmReply, error) {
	return api.Call[api.ZoneRmReply](context.Background(), c, http.MethodPost, api.PathZoneRm, api.ZoneRmRequest{Name: name, WithRoutes: with})
}

func zoneRmFixture(t *testing.T) (*fixture, *closedRoutes) {
	t.Helper()
	closed := new(closedRoutes)
	f := startConfigured(t, "1.2.3", api.ActorLocal, func(c *server.Config) { c.Conns = closed })
	for _, z := range []string{"example.com", "lab.example.com"} {
		if _, err := addZone(f.client, z); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := addOutpost(f.client, "nas"); err != nil {
		t.Fatal(err)
	}
	return f, closed
}

func hostnames(f *fixture) []string {
	var hs []string
	for _, r := range f.store.Snapshot().Routes {
		hs = append(hs, r.Hostname)
	}
	slices.Sort(hs)
	return hs
}

func TestZoneRmMovesRoutesToTheParentZone(t *testing.T) {
	f, closed := zoneRmFixture(t)
	if _, err := addRoute(f.client, "app.lab.example.com", "http://a:1", "nas"); err != nil {
		t.Fatal(err)
	}
	r, err := rmZone(f.client, "lab.example.com", false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Moved, []string{"app.lab.example.com"}) || len(r.Removed) != 0 {
		t.Errorf("reply %+v", r)
	}
	if got := hostnames(f); !slices.Equal(got, []string{"app.lab.example.com"}) {
		t.Errorf("Routes %v", got)
	}
	if z := f.store.Snapshot().Zones; len(z) != 1 || z[0].Name != "example.com" {
		t.Errorf("Zones %+v", z)
	}
	if len(closed.hosts) != 0 {
		t.Errorf("closed %v: a moved Route keeps its connections", closed.hosts)
	}
	if !strings.Contains(f.log.String(), "zone removed") || !strings.Contains(f.log.String(), "actor=local") {
		t.Errorf("log %s", f.log.String())
	}
}

func TestZoneRmRefusesToStrandRoutesAndChangesNothing(t *testing.T) {
	f, closed := zoneRmFixture(t)
	for _, h := range []string{"www.example.com", "app.lab.example.com", "git.example.com"} {
		if _, err := addRoute(f.client, h, "http://a:1", "nas"); err != nil {
			t.Fatal(err)
		}
	}
	// Removing the parent leaves www and git in no Zone; app stays in lab.
	_, err := rmZone(f.client, "example.com", false)
	var e *api.Error
	if !errors.As(err, &e) || e.Status != http.StatusConflict {
		t.Fatalf("err=%v", err)
	}
	for _, want := range []string{"git.example.com", "www.example.com", "--with-routes"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("refusal %q lacks %q", e.Message, want)
		}
	}
	if strings.Contains(e.Message, "app.lab") {
		t.Errorf("refusal names a Route the nested Zone still holds: %q", e.Message)
	}
	if len(f.store.Snapshot().Zones) != 2 || len(hostnames(f)) != 3 || len(closed.hosts) != 0 {
		t.Error("the refusal changed something")
	}
}

func TestZoneRmWithRoutesRemovesThemAndClosesTheirConnections(t *testing.T) {
	f, closed := zoneRmFixture(t)
	for _, h := range []string{"www.example.com", "app.lab.example.com"} {
		if _, err := addRoute(f.client, h, "http://a:1", "nas"); err != nil {
			t.Fatal(err)
		}
	}
	r, err := rmZone(f.client, "example.com", true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Removed, []string{"www.example.com"}) || len(r.Moved) != 0 {
		t.Errorf("reply %+v", r)
	}
	if got := hostnames(f); !slices.Equal(got, []string{"app.lab.example.com"}) {
		t.Errorf("Routes %v", got)
	}
	if !slices.Equal(closed.hosts, []string{"www.example.com"}) {
		t.Errorf("closed %v", closed.hosts)
	}
}

func TestZoneRmOfAnUnknownZoneIsA404AndAnOutpostMayNotRemoveZones(t *testing.T) {
	f, _ := zoneRmFixture(t)
	_, err := rmZone(f.client, "nope.test", false)
	var e *api.Error
	if !errors.As(err, &e) || e.Status != http.StatusNotFound {
		t.Fatalf("err=%v", err)
	}
	o := start(t, "1.2.3", api.OutpostActor("nas"))
	_, err = rmZone(o.client, "example.com", false)
	if !errors.As(err, &e) || e.Status != http.StatusForbidden {
		t.Fatalf("err=%v", err)
	}
}
