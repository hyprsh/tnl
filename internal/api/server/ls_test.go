package server_test

import (
	"context"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
	"github.com/hyprsh/tnl/internal/declare"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/state"
)

type certStatus struct {
	now     time.Time
	by      map[string]server.CertState
	covered map[string]bool
}

func (f *certStatus) Certificate(name string) server.CertState {
	st := f.by[name]
	st.Managed = true
	return st
}
func (f *certStatus) Covered(host string) bool { return f.covered[host] }
func (f *certStatus) Now() time.Time           { return f.now }

var lsNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

// lsFixture serves an Edge whose clock is lsNow, with the Outposts joined (no
// join token pending) except those named in pending.
func lsFixture(t *testing.T, live server.Liveness, certs server.CertReader, outposts ...string) *fixture {
	t.Helper()
	f := startConfigured(t, "1.2.3", api.ActorLocal, func(c *server.Config) {
		c.Resolver = edgeResolver()
		c.Liveness, c.CertReader = live, certs
		c.Now = func() time.Time { return lsNow }
	})
	if _, err := addZone(f.client, "example.com"); err != nil {
		t.Fatal(err)
	}
	for _, o := range outposts {
		if _, err := addOutpost(f.client, o); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// join marks the Outposts as having used their tokens.
func join(t *testing.T, f *fixture, names ...string) {
	t.Helper()
	err := f.store.Update(api.ActorLocal, func(st *state.State) error {
		st.Outposts = append([]state.Outpost(nil), st.Outposts...)
		for i := range st.Outposts {
			for _, n := range names {
				if st.Outposts[i].Name == n {
					st.Outposts[i].OneTimeKey, st.Outposts[i].TokenExpires = nil, time.Time{}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func lsOutposts(t *testing.T, f *fixture) map[string]api.OutpostInfo {
	t.Helper()
	l, err := api.Call[api.OutpostList](context.Background(), f.client, http.MethodGet, api.PathOutposts, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]api.OutpostInfo{}
	prev := ""
	for _, o := range l.Outposts {
		if o.Name < prev {
			t.Errorf("%s listed after %s", o.Name, prev)
		}
		prev = o.Name
		out[o.Name] = o
	}
	return out
}

func TestOutpostLsStatesAndWhatEachRowKnows(t *testing.T) {
	clock := lsNow
	live := liveness.New(liveness.Config{EdgeVersion: "1.2.3", OfflineAfter: time.Minute, Now: func() time.Time { return clock }})
	f := lsFixture(t, live, nil, "restarted", "heard", "gone", "old", "newbox", "stranger")
	join(t, f, "restarted", "heard", "gone", "old", "stranger")

	live.Seed("restarted")
	live.Seed("gone")
	live.Heard("gone")
	clock = clock.Add(50 * time.Second)
	live.Heard("old")
	live.SetVersion("old", "2.0.0")
	live.Heard("heard")
	live.SetVersion("heard", "1.2.3")
	live.SetEndpoint("heard", netip.MustParseAddrPort("203.0.113.9:51820"))
	clock = clock.Add(30 * time.Second) // gone is silent for 80 s, old and heard for 30 s; restarted was seeded 80 s ago

	got := lsOutposts(t, f)
	check := func(name string, state api.OutpostState, ago *int64) {
		t.Helper()
		o := got[name]
		if o.State != state {
			t.Errorf("%s: state %q, want %q", name, o.State, state)
		}
		switch {
		case ago == nil && o.LastSeenAgoSeconds != nil:
			t.Errorf("%s: last seen %d s ago, want none", name, *o.LastSeenAgoSeconds)
		case ago != nil && (o.LastSeenAgoSeconds == nil || *o.LastSeenAgoSeconds != *ago):
			t.Errorf("%s: last seen %v, want %d s ago", name, o.LastSeenAgoSeconds, *ago)
		}
	}
	eighty, thirty := int64(80), int64(30)
	check("heard", api.StateOnline, &thirty)
	check("gone", api.StateOffline, &eighty)
	check("old", api.StateIncompatible, &thirty)
	check("newbox", api.StateNotJoined, nil)
	// Seeded but never heard, and past the window: offline, with nothing seen.
	check("restarted", api.StateOffline, nil)
	// Joined, but liveness has never heard of it: offline too.
	check("stranger", api.StateOffline, nil)

	if o := got["heard"]; o.Endpoint != "203.0.113.9:51820" || o.Version != "1.2.3" || o.LastSeen == nil {
		t.Errorf("heard: %+v", o)
	}
	if o := got["newbox"]; o.TokenExpires == nil || o.TokenExpiresInSeconds == nil {
		t.Errorf("newbox: %+v", o)
	}
}

func TestAnOutpostSeededAtTheEdgesStartIsOnlineWithNothingSeen(t *testing.T) {
	live := liveness.New(liveness.Config{Now: func() time.Time { return lsNow }})
	f := lsFixture(t, live, nil, "nas")
	join(t, f, "nas")
	live.Seed("nas")
	o := lsOutposts(t, f)["nas"]
	if o.State != api.StateOnline || o.LastSeen != nil || o.LastSeenAgoSeconds != nil {
		t.Fatalf("%+v", o)
	}
}

func TestOutpostLsCountsRoutesAndMarksDeclared(t *testing.T) {
	f := lsFixture(t, nil, nil, "plain", "decl")
	join(t, f, "plain", "decl")
	err := f.store.Update(api.ActorLocal, func(st *state.State) error {
		st.Outposts = append([]state.Outpost(nil), st.Outposts...)
		for i := range st.Outposts {
			if st.Outposts[i].Name == "decl" {
				st.Outposts[i].Declared, st.Outposts[i].Address = true, addr.Derive("decl")
			}
		}
		st.Declarations = []state.Declaration{{Outpost: "decl", Lines: []state.DeclaredLine{
			{Line: declare.Line{Hostname: "a.example.com", Service: "http://a:1"}, Declared: lsNow},
			{Line: declare.Line{Hostname: "b.example.com", Service: "http://b:1"}, Declared: lsNow},
			{Line: declare.Line{Hostname: "c.other.org", Service: "http://c:1"}, Declared: lsNow},
		}}}
		st.Routes = []state.Route{
			{Hostname: "a.example.com", Outpost: "decl", Service: "http://a:1"},
			{Hostname: "b.example.com", Outpost: "decl", Service: "http://b:1"},
			{Hostname: "z.example.com", Outpost: "plain", Service: "http://z:1"},
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := lsOutposts(t, f)
	if o := got["decl"]; !o.Declared || o.DeclaredLines != 3 || o.Routes != 2 || o.State != api.StateOnline {
		t.Errorf("decl: %+v", o)
	}
	if o := got["plain"]; o.Declared || o.DeclaredLines != 0 || o.Routes != 1 {
		t.Errorf("plain: %+v", o)
	}
}

func TestAnOutpostMayNotListOutposts(t *testing.T) {
	f := start(t, "1.2.3", api.OutpostActor("nas"))
	_, err := api.Call[api.OutpostList](context.Background(), f.client, http.MethodGet, api.PathOutposts, nil)
	if err == nil {
		t.Fatal("an Outpost listed the Outposts")
	}
}

func lsRoutes(t *testing.T, f *fixture) []api.RouteRow {
	t.Helper()
	l, err := api.Call[api.RouteList](context.Background(), f.client, http.MethodGet, api.PathRoutes, nil)
	if err != nil {
		t.Fatal(err)
	}
	return l.Routes
}

func TestRouteLsStatusFollowsTheSpecsOrderOfProblems(t *testing.T) {
	clock := lsNow
	live := liveness.New(liveness.Config{EdgeVersion: "1.2.3", OfflineAfter: time.Minute, Now: func() time.Time { return clock }})
	certs := &certStatus{now: lsNow, by: map[string]server.CertState{}, covered: map[string]bool{}}
	f := lsFixture(t, live, certs, "up", "down", "old", "newbox")
	join(t, f, "up", "down", "old")
	live.Heard("down")
	clock = clock.Add(2 * time.Minute)
	live.Heard("up")
	live.Heard("old")
	live.SetVersion("old", "2.0.0")

	fresh := lsNow.Add(30 * 24 * time.Hour)
	certs.now = clock
	certs.by["ok.example.com"] = server.CertState{NotAfter: fresh}
	certs.by["expired.example.com"] = server.CertState{NotAfter: lsNow.Add(-time.Hour)}
	certs.by["renew.example.com"] = server.CertState{NotAfter: fresh, Failures: 2, FirstFailure: lsNow, LastError: "boom"}
	certs.by["both.example.com"] = server.CertState{NotAfter: lsNow.Add(-time.Hour), Failures: 1}
	certs.covered["wild.example.com"] = true
	err := f.store.Update(api.ActorLocal, func(st *state.State) error {
		st.Routes = append(st.Routes, []state.Route{
			{Hostname: "ok.example.com", Outpost: "up", Service: "http://x:1"},
			{Hostname: "none.example.com", Outpost: "up", Service: "http://x:1"},
			{Hostname: "expired.example.com", Outpost: "up", Service: "http://x:1"},
			{Hostname: "renew.example.com", Outpost: "up", Service: "http://x:1"},
			{Hostname: "both.example.com", Outpost: "up", Service: "http://x:1"},
			{Hostname: "wild.example.com", Outpost: "up", Service: "http://x:1"},
			{Hostname: "off.example.com", Outpost: "down", Service: "http://x:1"},
			{Hostname: "inc.example.com", Outpost: "old", Service: "http://x:1"},
			{Hostname: "nj.example.com", Outpost: "newbox", Service: "http://x:1"},
		}...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]api.RouteStatus{
		"ok.example.com":      api.StatusOK,
		"none.example.com":    api.StatusNoCertificate,
		"expired.example.com": api.StatusCertExpired,
		"renew.example.com":   api.StatusRenewalFailing,
		"both.example.com":    api.StatusCertExpired, // expired comes before failing
		"wild.example.com":    api.StatusOK,
		"off.example.com":     api.StatusOutpostOffline,
		"inc.example.com":     api.StatusOutpostIncompatible,
		"nj.example.com":      api.StatusOutpostNotJoined,
	}
	rows := lsRoutes(t, f)
	if len(rows) != len(want) {
		t.Fatalf("%d rows: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.Status != want[r.Hostname] {
			t.Errorf("%s: %q, want %q", r.Hostname, r.Status, want[r.Hostname])
		}
	}
}

func TestRouteLsOutpostProblemsComeBeforeCertificateOnes(t *testing.T) {
	certs := &certStatus{now: lsNow, by: map[string]server.CertState{}}
	live := liveness.New(liveness.Config{Now: func() time.Time { return lsNow }})
	f := lsFixture(t, live, certs, "gone")
	join(t, f, "gone")
	err := f.store.Update(api.ActorLocal, func(st *state.State) error {
		st.Routes = []state.Route{{Hostname: "a.example.com", Outpost: "gone", Service: "http://x:1"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows := lsRoutes(t, f); len(rows) != 1 || rows[0].Status != api.StatusOutpostOffline {
		t.Fatalf("%+v", rows)
	}
}

func TestRouteLsShowsWaitingLinesAndAMoveAsTwoRows(t *testing.T) {
	f := lsFixture(t, nil, nil, "nas", "newbox")
	join(t, f, "nas", "newbox")
	err := f.store.Update(api.ActorLocal, func(st *state.State) error {
		st.Routes = []state.Route{{Hostname: "photos.example.com", Outpost: "nas", Service: "http://nas:2283"}}
		st.Declarations = []state.Declaration{{Outpost: "newbox", Lines: []state.DeclaredLine{
			{Line: declare.Line{Hostname: "photos.example.com", Service: "http://new:2283", HostHeader: "photos.lan"}, Declared: lsNow},
			{Line: declare.Line{Hostname: "x.other.org", Service: "http://new:1"}, Declared: lsNow},
		}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := lsRoutes(t, f)
	if len(rows) != 3 {
		t.Fatalf("%+v", rows)
	}
	// By Hostname; for a move, the Route first and then the line that waits.
	if r := rows[0]; r.Hostname != "photos.example.com" || r.Outpost != "nas" || r.Waiting != "" || r.Status != api.StatusOK {
		t.Errorf("row 0: %+v", r)
	}
	if r := rows[1]; r.Hostname != "photos.example.com" || r.Outpost != "newbox" || r.Waiting != "taken by nas" ||
		r.Status != "waiting: taken by nas" || r.Service != "http://new:2283" || len(r.Options) != 1 {
		t.Errorf("row 1: %+v", r)
	}
	if r := rows[2]; r.Hostname != "x.other.org" || r.Waiting != "outside every Zone" || r.Status != "waiting: outside every Zone" {
		t.Errorf("row 2: %+v", r)
	}
}

func TestZoneLsCertificateForWildcardAndPerHostnameZones(t *testing.T) {
	fresh := lsNow.Add(30 * 24 * time.Hour)
	certs := &certStatus{now: lsNow, covered: map[string]bool{"covered.wild.test": true}, by: map[string]server.CertState{
		"*.wild.test":   {NotAfter: fresh, Failures: 1, FirstFailure: lsNow.Add(-72 * time.Hour), LastError: "rate limited\nretry later"},
		"a.example.com": {NotAfter: fresh},
		"b.example.com": {NotAfter: fresh, Failures: 1, FirstFailure: lsNow.Add(-time.Hour), LastError: "x"},
	}}
	f := lsFixture(t, nil, certs, "nas")
	err := f.store.Update(api.ActorLocal, func(st *state.State) error {
		st.Zones = append(st.Zones, state.Zone{Name: "wild.test", Mode: state.Wildcard, Provider: "cloudflare", Token: "secret", Declared: true})
		st.Routes = []state.Route{
			{Hostname: "b.example.com", Outpost: "nas", Service: "http://x:1"},
			{Hostname: "a.example.com", Outpost: "nas", Service: "http://x:1"},
			{Hostname: "covered.wild.test", Outpost: "nas", Service: "http://x:1"},
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := api.Call[api.ZoneList](context.Background(), f.client, http.MethodGet, api.PathZones, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Zones) != 2 || l.Zones[0].Name != "example.com" || l.Zones[1].Name != "wild.test" {
		t.Fatalf("%+v", l.Zones)
	}
	per, wild := l.Zones[0], l.Zones[1]
	if len(per.Certificates) != 2 || per.Certificates[0].Name != "a.example.com" || per.Certificates[0].State != api.CertValid ||
		per.Certificates[1].State != api.CertRenewalFailing || per.Certificates[1].Error != "x" {
		t.Errorf("per-Hostname: %+v", per)
	}
	c := wild.Certificate
	if !wild.Declared || c == nil || c.Name != "*.wild.test" || c.State != api.CertRenewalFailing || c.Error != "rate limited\nretry later" ||
		c.FailingForSeconds == nil || *c.FailingForSeconds != 72*3600 || c.Expires == nil || !c.Expires.Equal(fresh) {
		t.Errorf("wildcard: %+v", wild)
	}
	if len(wild.Certificates) != 0 {
		t.Errorf("a wildcard Zone lists no per-Hostname certificates: %+v", wild.Certificates)
	}
}

func TestZoneLsWithoutACertificateManagerReadsAsObtaining(t *testing.T) {
	f := lsFixture(t, nil, nil)
	err := f.store.Update(api.ActorLocal, func(st *state.State) error {
		st.Zones = append(st.Zones, state.Zone{Name: "wild.test", Mode: state.Wildcard, Provider: "cloudflare", Token: "t"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	l, _ := api.Call[api.ZoneList](context.Background(), f.client, http.MethodGet, api.PathZones, nil)
	if c := l.Zones[1].Certificate; c == nil || c.State != api.CertObtaining {
		t.Fatalf("%+v", l.Zones)
	}
}
