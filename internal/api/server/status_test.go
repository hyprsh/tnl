package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
	"github.com/hyprsh/tnl/internal/declare"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/state"
)

func getStatus(t *testing.T, f *fixture) api.EdgeStatus {
	t.Helper()
	s, err := api.Call[api.EdgeStatus](context.Background(), f.client, http.MethodGet, api.PathStatus, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func attention(s api.EdgeStatus) string {
	var lines []string
	for _, a := range s.Attention {
		lines = append(lines, string(a.Kind)+": "+a.Message)
	}
	return strings.Join(lines, "\n")
}

func TestStatusOfAHealthyEdgeHasASummaryAndNothingToAttendTo(t *testing.T) {
	live := liveness.New(liveness.Config{EdgeVersion: "1.2.3", OfflineAfter: time.Minute, Now: func() time.Time { return lsNow }})
	certs := &certStatus{now: lsNow, by: map[string]server.CertState{
		"app.example.com": {NotAfter: lsNow.Add(30 * 24 * time.Hour)},
	}}
	start := lsNow.Add(-90 * time.Minute)
	f := startConfigured(t, "1.2.3", api.ActorLocal, func(c *server.Config) {
		c.Resolver = edgeResolver()
		c.Liveness, c.CertReader, c.Now = live, certs, func() time.Time { return lsNow }
		c.StartedAt, c.LogLevel, c.AccessLog = start, "debug", true
	})
	if _, err := addZone(f.client, "example.com"); err != nil {
		t.Fatal(err)
	}
	for _, o := range []string{"nas", "newbox"} {
		if _, err := addOutpost(f.client, o); err != nil {
			t.Fatal(err)
		}
	}
	join(t, f, "nas")
	live.Heard("nas")
	if _, err := addRoute(f.client, "app.example.com", "http://localhost:3000", "nas"); err != nil {
		t.Fatal(err)
	}

	s := getStatus(t, f)
	if s.Role != api.RoleEdge || s.Version != "1.2.3" || s.Build != "full" || s.Revision != "abc123" ||
		s.Address != "tnl.example.com" || s.WireGuardPort != 51820 || s.StateDir != f.dir ||
		s.LogLevel != "debug" || !s.AccessLog || s.UptimeSeconds != 5400 || !s.Started.Equal(start) ||
		!strings.HasPrefix(s.KeyFingerprint, "SHA256:") {
		t.Errorf("summary: %+v", s)
	}
	if s.Outposts != (api.OutpostCounts{Online: 1, NotJoined: 1}) || s.Routes != 1 || s.Zones != 1 {
		t.Errorf("counts: %+v", s)
	}
	if s.Certificates.Count != 1 || s.Certificates.SoonestExpiry == nil || !s.Certificates.SoonestExpiry.Equal(lsNow.Add(30*24*time.Hour)) {
		t.Errorf("certificates: %+v", s.Certificates)
	}
	if len(s.Attention) != 0 {
		t.Errorf("attention:\n%s", attention(s))
	}
}

func TestStatusNamesEachProblem(t *testing.T) {
	clock := lsNow
	live := liveness.New(liveness.Config{EdgeVersion: "1.2.3", OfflineAfter: time.Minute, Now: func() time.Time { return clock }})
	certs := &certStatus{now: lsNow, by: map[string]server.CertState{
		"soon.example.com":    {NotAfter: lsNow.Add(3 * 24 * time.Hour)},
		"later.example.com":   {NotAfter: lsNow.Add(8 * 24 * time.Hour)},
		"failing.example.com": {NotAfter: lsNow.Add(20 * 24 * time.Hour), Failures: 2, FirstFailure: lsNow.Add(-3 * time.Hour), LastError: "rate limited\nretry"},
	}}
	f := lsFixture(t, live, certs, "nas", "gone", "old", "newbox")
	join(t, f, "nas", "gone", "old", "newbox")
	live.Heard("nas")
	live.Heard("gone")
	live.Heard("old")
	live.SetVersion("old", "2.0.0")
	clock = clock.Add(4 * time.Minute)
	live.Heard("nas")
	live.Heard("old")
	live.SetVersion("old", "2.0.0")
	err := f.store.Update(api.ActorLocal, func(st *state.State) error {
		st.Routes = []state.Route{
			{Hostname: "photos.example.com", Outpost: "nas", Service: "http://nas:2283"},
			{Hostname: "soon.example.com", Outpost: "gone", Service: "http://g:1"},
			{Hostname: "later.example.com", Outpost: "nas", Service: "http://g:2"},
			{Hostname: "failing.example.com", Outpost: "nas", Service: "http://g:3"},
		}
		st.Declarations = []state.Declaration{{Outpost: "newbox", Lines: []state.DeclaredLine{
			{Line: declare.Line{Hostname: "photos.example.com", Service: "http://new:2283"}, Declared: lsNow},
		}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	s := getStatus(t, f)
	if s.Outposts != (api.OutpostCounts{Online: 1, Offline: 2, Incompatible: 1}) {
		t.Errorf("counts: %+v", s.Outposts)
	}
	if s.Routes != 4 {
		t.Errorf("routes %d", s.Routes)
	}
	got := attention(s)
	for _, want := range []string{
		"outpost offline: Outpost gone is offline, last seen 4m ago, 1 Route",
		"outpost offline: Outpost newbox is offline, not heard from since the Edge started, 0 Routes",
		"outpost incompatible: Outpost old is incompatible, last seen 0s ago, 0 Routes",
		"certificate expiring: certificate soon.example.com expires in 3d",
		"certificate failing: certificate failing.example.com: renewal failing for 3h, expires in 20d: rate limited",
		"line waiting: newbox declares photos.example.com, which nas holds",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "later.example.com") {
		t.Errorf("a certificate with 8 days left needs no attention:\n%s", got)
	}
}

func TestStatusIsRefusedToAnOutpost(t *testing.T) {
	f := startConfigured(t, "1.2.3", api.OutpostActor("nas"), func(c *server.Config) {})
	_, err := api.Call[api.EdgeStatus](context.Background(), f.client, http.MethodGet, api.PathStatus, nil)
	if err == nil || !strings.Contains(err.Error(), "may not") {
		t.Fatalf("got %v, want a refusal", err)
	}
}
