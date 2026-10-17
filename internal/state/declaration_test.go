package state

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/declare"
)

func line(host, svc string) declare.Line { return declare.Line{Hostname: host, Service: svc} }

// declState has Zone example.com and Outposts nas and newbox, with a Route of
// nas's own made with the CLI.
func declState(t *testing.T) *State {
	t.Helper()
	st := &State{Version: Version}
	st.Zones = []Zone{{Name: "example.com", Mode: PerHostname}}
	st.Outposts = []Outpost{
		{Name: "nas", PublicKey: mustKey(t), Address: st.AllocAddr()},
		{Name: "newbox", PublicKey: mustKey(t), Address: st.AllocAddr()},
	}
	st.Routes = []Route{{Hostname: "cli.example.com", Outpost: "nas", Service: "http://localhost:1"}}
	return st
}

func TestApplyingTheFirstDeclarationReplacesTheCLIRoutes(t *testing.T) {
	st := declState(t)
	res := st.ApplyDeclaration("nas", []declare.Line{line("a.example.com", "http://localhost:2")}, time.Unix(100, 0))
	if !reflect.DeepEqual(res.Switched, []string{"cli.example.com"}) || !reflect.DeepEqual(res.Added, []string{"a.example.com"}) {
		t.Fatalf("result %+v", res)
	}
	if _, ok := st.Route("cli.example.com"); ok {
		t.Fatal("the CLI Route stayed")
	}
	if !st.Declares("nas") {
		t.Fatal("nas does not declare")
	}
	if err := st.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyingADeclarationIsADiffByHostname(t *testing.T) {
	st := declState(t)
	st.ApplyDeclaration("nas", []declare.Line{line("a.example.com", "http://localhost:2"), line("b.example.com", "http://localhost:3"), line("c.example.com", "http://localhost:4")}, time.Unix(100, 0))
	next := []declare.Line{
		line("a.example.com", "http://localhost:2"),                                 // unchanged
		{Hostname: "b.example.com", Service: "http://localhost:3", HostHeader: "h"}, // option only
		line("d.example.com", "http://localhost:5"),                                 // new; c is gone
	}
	res := st.ApplyDeclaration("nas", next, time.Unix(200, 0))
	want := DeclarationResult{
		Added: []string{"d.example.com"}, Changed: []string{"b.example.com"}, Unchanged: []string{"a.example.com"},
		Removed: []string{"c.example.com"}, Close: []string{"b.example.com", "c.example.com"},
		Dial: []Route{{Hostname: "d.example.com", Outpost: "nas", Service: "http://localhost:5"}},
	}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("got  %+v\nwant %+v", res, want)
	}
	if len(st.Routes) != 3 {
		t.Fatalf("Routes %+v", st.Routes)
	}
	// Times of lines that stay are kept; a new line gets the new time.
	times := map[string]time.Time{}
	for _, l := range st.DeclarationOf("nas") {
		times[l.Hostname] = l.Declared
	}
	if !times["a.example.com"].Equal(time.Unix(100, 0)) || !times["d.example.com"].Equal(time.Unix(200, 0)) {
		t.Fatalf("times %v", times)
	}
}

func TestALineChangingOnlyItsServiceIsTestDialed(t *testing.T) {
	st := declState(t)
	st.ApplyDeclaration("nas", []declare.Line{line("a.example.com", "http://localhost:2")}, time.Unix(1, 0))
	res := st.ApplyDeclaration("nas", []declare.Line{line("a.example.com", "http://localhost:9")}, time.Unix(2, 0))
	if len(res.Dial) != 1 || res.Dial[0].Service != "http://localhost:9" {
		t.Fatalf("dial %+v", res.Dial)
	}
}

func TestLinesThatCantApplyWaitAndTheRestApply(t *testing.T) {
	st := declState(t)
	st.ApplyDeclaration("nas", []declare.Line{line("a.example.com", "http://localhost:2")}, time.Unix(1, 0))
	res := st.ApplyDeclaration("newbox", []declare.Line{
		line("a.example.com", "http://localhost:3"), // held by nas
		line("b.other.org", "http://localhost:4"),   // outside every Zone
		line("c.example.com", "http://localhost:5"),
	}, time.Unix(2, 0))
	if !reflect.DeepEqual(res.Added, []string{"c.example.com"}) {
		t.Fatalf("added %v", res.Added)
	}
	want := []WaitingLine{{"a.example.com", "held by Outpost nas"}, {"b.other.org", "outside every Zone"}}
	if !reflect.DeepEqual(res.Waiting, want) {
		t.Fatalf("waiting %+v", res.Waiting)
	}
	entries := st.Entries("newbox")
	if len(entries) != 3 || !entries[0].Waiting || !entries[1].Waiting || entries[2].Waiting {
		t.Fatalf("entries %+v", entries)
	}
	if err := st.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAnEmptyDeclarationHandsTheOutpostBack(t *testing.T) {
	st := declState(t)
	st.ApplyDeclaration("nas", []declare.Line{line("a.example.com", "http://localhost:2")}, time.Unix(1, 0))
	res := st.ApplyDeclaration("nas", nil, time.Unix(2, 0))
	if !reflect.DeepEqual(res.Removed, []string{"a.example.com"}) || len(res.Switched) != 0 || st.Declares("nas") || len(st.Routes) != 0 {
		t.Fatalf("result %+v, state %+v", res, st)
	}
	// An empty Declaration from an Outpost on the CLI changes nothing.
	st = declState(t)
	res = st.ApplyDeclaration("nas", nil, time.Unix(2, 0))
	if len(st.Routes) != 1 || len(res.Close) != 0 {
		t.Fatalf("result %+v, routes %+v", res, st.Routes)
	}
}

func TestDeclarationsRoundTripAndAreCheckedWhole(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	addSample(t, s)
	err := s.Update("local", func(st *State) error {
		st.ApplyDeclaration("home", []declare.Line{{Hostname: "app.example.com", Service: "http://localhost:3000", HostHeader: "x", InsecureSkipVerify: true}, line("w.other.org", "http://localhost:1")}, time.Date(2026, 10, 1, 12, 0, 0, 123, time.UTC))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := open(t, dir)
	got := s2.Snapshot().DeclarationOf("home")
	if len(got) != 2 || got[0].HostHeader != "x" || !got[0].InsecureSkipVerify || !got[1].Declared.Equal(time.Date(2026, 10, 1, 12, 0, 0, 123, time.UTC)) {
		t.Fatalf("lines %+v", got)
	}

	// A Route of a declaring Outpost that no line asks for is refused.
	err = s2.Update("local", func(st *State) error {
		st.Routes = append(st.Routes, Route{Hostname: "extra.example.com", Outpost: "home", Service: "http://x:1"})
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "extra.example.com") {
		t.Fatalf("err = %v", err)
	}
	// A Declaration of an Outpost that does not exist is refused.
	err = s2.Update("local", func(st *State) error {
		st.Declarations = append(st.Declarations, Declaration{Outpost: "ghost", Lines: []DeclaredLine{{Line: line("g.example.com", "http://x:1"), Declared: time.Now()}}})
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("err = %v", err)
	}
}

func TestSnapshotsKeepTheirDeclarations(t *testing.T) {
	s, _ := open(t, t.TempDir())
	addSample(t, s)
	s.Update("local", func(st *State) error {
		st.ApplyDeclaration("home", []declare.Line{line("app.example.com", "http://localhost:3000")}, time.Unix(1, 0))
		return nil
	})
	old := s.Snapshot()
	s.Update("local", func(st *State) error {
		st.Declarations[0].Lines[0].HostHeader = "changed"
		return nil
	})
	if old.Declarations[0].Lines[0].HostHeader != "" {
		t.Fatal("an old snapshot was mutated")
	}
}

func TestAChangedHeaderOrNoHSTSEditsTheRouteAndClosesItsConnections(t *testing.T) {
	st := declState(t)
	st.ApplyDeclaration("nas", []declare.Line{line("a.example.com", "http://localhost:2")}, time.Unix(100, 0))
	next := []declare.Line{{Hostname: "a.example.com", Service: "http://localhost:2", NoHSTS: true, Headers: []string{"X-A: b"}}}
	res := st.ApplyDeclaration("nas", next, time.Unix(200, 0))
	if !reflect.DeepEqual(res.Changed, []string{"a.example.com"}) || !reflect.DeepEqual(res.Close, []string{"a.example.com"}) || len(res.Dial) != 0 {
		t.Fatalf("%+v", res)
	}
	if r, _ := st.Route("a.example.com"); !r.NoHSTS || !reflect.DeepEqual(r.Headers, []string{"X-A: b"}) {
		t.Fatalf("%+v", r)
	}
	if res = st.ApplyDeclaration("nas", next, time.Unix(300, 0)); !reflect.DeepEqual(res.Unchanged, []string{"a.example.com"}) {
		t.Fatalf("the same line again: %+v", res)
	}
}
