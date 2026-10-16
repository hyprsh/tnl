package state

import (
	"log/slog"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/declare"
	"github.com/hyprsh/tnl/internal/keys"
)

func apply(t *testing.T, s *Store, zones []DeclaredZone, outposts []DeclaredOutpost) (DeclaredResult, error) {
	t.Helper()
	var res DeclaredResult
	err := s.Update("local", func(st *State) (err error) {
		res, err = st.ApplyDeclared(zones, outposts)
		return err
	})
	return res, err
}

func hasEvent(res DeclaredResult, level slog.Level, parts ...string) bool {
next:
	for _, e := range res.Events {
		if e.Level != level {
			continue
		}
		for _, p := range parts {
			if !strings.Contains(e.Msg, p) {
				continue next
			}
		}
		return true
	}
	return false
}

func TestANewZoneLineAddsAMarkedZoneAndAWildcardIsToBeChecked(t *testing.T) {
	s, _ := open(t, filepath.Join(t.TempDir(), "tnl"))
	res, err := apply(t, s, []DeclaredZone{{Name: "example.com"}, {Name: "wild.org", Provider: "p", Token: "tok"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := s.Snapshot()
	if len(st.Zones) != 2 || !st.Zones[0].Declared || !st.Zones[1].Declared || st.Zones[1].Mode != Wildcard {
		t.Fatalf("zones %+v", st.Zones)
	}
	if len(res.Checks) != 2 {
		t.Fatalf("zones to check: %+v", res.Checks)
	}
	// Unchanged on a second pass: nothing to log, nothing to check.
	res, err = apply(t, s, []DeclaredZone{{Name: "example.com"}, {Name: "wild.org", Provider: "p", Token: "tok"}}, nil)
	if err != nil || len(res.Events) != 0 || len(res.Checks) != 0 {
		t.Fatalf("second pass: %+v, %v", res, err)
	}
}

func TestAChangedTokenOrModeIsAZoneEdit(t *testing.T) {
	s, _ := open(t, filepath.Join(t.TempDir(), "tnl"))
	apply(t, s, []DeclaredZone{{Name: "example.com", Provider: "p", Token: "one"}}, nil)
	res, err := apply(t, s, []DeclaredZone{{Name: "example.com", Provider: "p", Token: "two"}}, nil)
	if err != nil || len(res.Checks) != 1 || !hasEvent(res, slog.LevelInfo, "zone edited") {
		t.Fatalf("%+v, %v", res, err)
	}
	if got := s.Snapshot().Zones[0].Token; got != "two" {
		t.Fatalf("token %q", got)
	}
	res, err = apply(t, s, []DeclaredZone{{Name: "example.com"}}, nil)
	if err != nil || len(res.Checks) != 1 || s.Snapshot().Zones[0].Mode != PerHostname || s.Snapshot().Zones[0].Token != "" {
		t.Fatalf("%+v, %v, %+v", res, err, s.Snapshot().Zones)
	}
}

func TestADeclaredZoneAdoptsTheCLIZoneOfThatName(t *testing.T) {
	s, _ := open(t, filepath.Join(t.TempDir(), "tnl"))
	addSample(t, s)
	res, err := apply(t, s, []DeclaredZone{{Name: "example.com"}}, nil)
	if err != nil || !s.Snapshot().Zones[0].Declared || !hasEvent(res, slog.LevelInfo, "adopted") {
		t.Fatalf("%+v, %v", res, err)
	}
	if len(s.Snapshot().Routes) != 1 {
		t.Fatal("adopting lost the Route")
	}
}

func TestAZoneLineThatIsGoneStrandingACLIRouteRefusesAndChangesNothing(t *testing.T) {
	s, _ := open(t, filepath.Join(t.TempDir(), "tnl"))
	addSample(t, s)
	apply(t, s, []DeclaredZone{{Name: "example.com"}, {Name: "other.org"}}, nil)
	before := s.Raw()
	_, err := apply(t, s, []DeclaredZone{{Name: "other.org"}}, nil)
	if err == nil {
		t.Fatal("accepted")
	}
	want := "example.com is no longer declared, but app.example.com is in it; remove them or declare the Zone again"
	if err.Error() != want {
		t.Fatalf("error %q, want %q", err, want)
	}
	if string(s.Raw()) != string(before) {
		t.Fatal("state.json changed")
	}
}

func TestAZoneLineThatIsGoneSendsDeclaredRoutesBackToWaitingAndParentsTakeOver(t *testing.T) {
	s, _ := open(t, filepath.Join(t.TempDir(), "tnl"))
	pub := mustKey(t)
	err := s.Update("local", func(st *State) error {
		st.Zones = []Zone{{Name: "example.com", Mode: PerHostname, Declared: true}, {Name: "sub.example.com", Mode: PerHostname, Declared: true}, {Name: "gone.org", Mode: PerHostname, Declared: true}}
		st.Outposts = []Outpost{{Name: "box", PublicKey: pub, Address: st.AllocAddr()}}
		st.Routes = []Route{
			{Hostname: "a.sub.example.com", Outpost: "box", Service: "http://x:1"},
			{Hostname: "b.gone.org", Outpost: "box", Service: "http://x:2"},
		}
		st.Declarations = []Declaration{{Outpost: "box", Lines: []DeclaredLine{
			{Line: declare.Line{Hostname: "a.sub.example.com", Service: "http://x:1"}, Declared: nowForTest},
			{Line: declare.Line{Hostname: "b.gone.org", Service: "http://x:2"}, Declared: nowForTest},
		}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := apply(t, s, []DeclaredZone{{Name: "example.com"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := s.Snapshot()
	if len(st.Routes) != 1 || st.Routes[0].Hostname != "a.sub.example.com" {
		t.Fatalf("routes %+v", st.Routes)
	}
	if len(res.Waiting) != 1 || res.Waiting[0] != "b.gone.org" || len(st.DeclarationOf("box")) != 2 {
		t.Fatalf("%+v", res)
	}
}

var nowForTest = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func TestAZoneLineThatIsGoneMovesRoutesToTheParentZone(t *testing.T) {
	s, _ := open(t, filepath.Join(t.TempDir(), "tnl"))
	pub := mustKey(t)
	if err := s.Update("local", func(st *State) error {
		st.Zones = []Zone{{Name: "example.com", Mode: PerHostname, Declared: true}, {Name: "sub.example.com", Mode: PerHostname, Declared: true}}
		st.Outposts = []Outpost{{Name: "box", PublicKey: pub, Address: st.AllocAddr()}}
		st.Routes = []Route{{Hostname: "a.sub.example.com", Outpost: "box", Service: "http://x:1"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	res, err := apply(t, s, []DeclaredZone{{Name: "example.com"}}, nil)
	if err != nil || len(s.Snapshot().Routes) != 1 || len(res.Removed) != 0 {
		t.Fatalf("%+v, %v", res, err)
	}
	if !hasEvent(res, slog.LevelWarn, "zone removed") {
		t.Fatal("no warn for the removal")
	}
}

func TestNoZoneLinesHandsTheZonesBackAndRemovesNothing(t *testing.T) {
	s, _ := open(t, filepath.Join(t.TempDir(), "tnl"))
	apply(t, s, []DeclaredZone{{Name: "example.com"}}, []DeclaredOutpost{{Name: "nas", Key: mustKey(t)}})
	res, err := apply(t, s, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := s.Snapshot()
	if len(st.Zones) != 1 || st.Zones[0].Declared || len(st.Outposts) != 1 || st.Outposts[0].Declared {
		t.Fatalf("%+v %+v", st.Zones, st.Outposts)
	}
	if !hasEvent(res, slog.LevelWarn, "TNL_ZONE_*") || !hasEvent(res, slog.LevelWarn, "TNL_OUTPOST_*") {
		t.Fatalf("events %+v", res.Events)
	}
	// The derived address is not below the counter, and the state is still valid.
	if st.Outposts[0].Address != addr.Derive("nas") {
		t.Fatalf("address %v", st.Outposts[0].Address)
	}
}

func TestANewOutpostLineAddsAJoinedOutpostAtItsDerivedAddress(t *testing.T) {
	s, _ := open(t, filepath.Join(t.TempDir(), "tnl"))
	k := mustKey(t)
	if _, err := apply(t, s, nil, []DeclaredOutpost{{Name: "nas", Key: k}}); err != nil {
		t.Fatal(err)
	}
	o := s.Snapshot().Outposts[0]
	if o.Address != addr.Derive("nas") || o.PublicKey != k || !o.Declared || o.OneTimeKey != nil || !o.TokenExpires.IsZero() {
		t.Fatalf("%+v", o)
	}
}

func TestAChangedKeyIsARekeyThatKeepsAddressAndRoutes(t *testing.T) {
	s, _ := open(t, filepath.Join(t.TempDir(), "tnl"))
	apply(t, s, []DeclaredZone{{Name: "example.com"}}, []DeclaredOutpost{{Name: "nas", Key: mustKey(t)}})
	s.Update("local", func(st *State) error {
		st.Outposts[0].Endpoint = netip.MustParseAddrPort("192.0.2.1:5000")
		st.Routes = append(st.Routes, Route{Hostname: "a.example.com", Outpost: "nas", Service: "http://x:1"})
		return nil
	})
	k2 := mustKey(t)
	res, err := apply(t, s, []DeclaredZone{{Name: "example.com"}}, []DeclaredOutpost{{Name: "nas", Key: k2}})
	if err != nil {
		t.Fatal(err)
	}
	st := s.Snapshot()
	o := st.Outposts[0]
	if o.PublicKey != k2 || o.Address != addr.Derive("nas") || o.Endpoint.IsValid() || len(st.Routes) != 1 {
		t.Fatalf("%+v routes %v", o, st.Routes)
	}
	if !hasEvent(res, slog.LevelInfo, "rekeyed") {
		t.Fatalf("events %+v", res.Events)
	}
}

func TestADeclaredNameAdoptsTheCLIOutpostAndMovesItToItsDerivedAddress(t *testing.T) {
	s, _ := open(t, filepath.Join(t.TempDir(), "tnl"))
	var oneTime keys.Key = mustKey(t)
	s.Update("local", func(st *State) error {
		st.Zones = []Zone{{Name: "example.com", Mode: PerHostname}}
		st.Outposts = []Outpost{{Name: "nas", PublicKey: oneTime, Address: st.AllocAddr(), OneTimeKey: &oneTime, TokenExpires: nowForTest}}
		st.Routes = []Route{{Hostname: "a.example.com", Outpost: "nas", Service: "http://x:1"}}
		return nil
	})
	k := mustKey(t)
	res, err := apply(t, s, nil, []DeclaredOutpost{{Name: "nas", Key: k}})
	if err != nil {
		t.Fatal(err)
	}
	st := s.Snapshot()
	o := st.Outposts[0]
	if !o.Declared || o.Address != addr.Derive("nas") || o.PublicKey != k || o.OneTimeKey != nil || len(st.Routes) != 1 {
		t.Fatalf("%+v", o)
	}
	if !hasEvent(res, slog.LevelInfo, "adopted") {
		t.Fatalf("events %+v", res.Events)
	}
}

func TestOutpostsThatAreNotDeclaredAreRemovedWithTheirRoutes(t *testing.T) {
	s, _ := open(t, filepath.Join(t.TempDir(), "tnl"))
	addSample(t, s) // CLI Outpost "home" with a Route
	res, err := apply(t, s, nil, []DeclaredOutpost{{Name: "nas", Key: mustKey(t)}})
	if err != nil {
		t.Fatal(err)
	}
	st := s.Snapshot()
	if len(st.Outposts) != 1 || st.Outposts[0].Name != "nas" || len(st.Routes) != 0 || len(res.Removed) != 1 {
		t.Fatalf("%+v %+v", st.Outposts, st.Routes)
	}
	if !hasEvent(res, slog.LevelWarn, "outpost removed") {
		t.Fatal("no warn for the removal")
	}
}

func TestTheCounterSkipsAddressesThatAreTaken(t *testing.T) {
	st := &State{Version: Version}
	st.Outposts = []Outpost{{Name: "x", Address: addr.Outpost(0)}, {Name: "y", Address: addr.Outpost(2)}}
	if a := st.AllocAddr(); a != addr.Outpost(1) {
		t.Fatalf("first %v", a)
	}
	if a := st.AllocAddr(); a != addr.Outpost(3) {
		t.Fatalf("second %v", a)
	}
}

func TestOnlyCounterAssignedAddressesMustBeBelowTheCounter(t *testing.T) {
	k := mustKey(t)
	st := &State{Version: Version, Outposts: []Outpost{{Name: "nas", PublicKey: k, Address: addr.Derive("nas"), Declared: true}}}
	if err := st.Validate(); err != nil {
		t.Fatalf("a derived address above the counter: %v", err)
	}
	st.Outposts = []Outpost{{Name: "cli", PublicKey: k, Address: addr.Outpost(5)}}
	if err := st.Validate(); err == nil || !strings.Contains(err.Error(), "counter") {
		t.Fatalf("a counter address above the counter: %v", err)
	}
	st.Outposts = []Outpost{{Name: "nas", PublicKey: k, Address: addr.Outpost(0), Declared: true}}
	if err := st.Validate(); err == nil || !strings.Contains(err.Error(), "derived") {
		t.Fatalf("a declared Outpost off its derived address: %v", err)
	}
}

func TestDeclaredMarksSurviveARestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tnl")
	s, _ := open(t, dir)
	apply(t, s, []DeclaredZone{{Name: "example.com", Provider: "p", Token: "t"}}, []DeclaredOutpost{{Name: "nas", Key: mustKey(t)}})
	again, _ := open(t, dir)
	st := again.Snapshot()
	if !st.Zones[0].Declared || !st.Outposts[0].Declared {
		t.Fatalf("%+v %+v", st.Zones, st.Outposts)
	}
}

func TestOpenWithKeyCreatesWithItAndRefusesAnotherLater(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tnl")
	priv, pub, _ := keys.Generate()
	lg, buf := newLog()
	s, err := OpenWithKey(dir, lg, &priv, "TNL_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().PrivateKey != priv || !strings.Contains(buf.String(), pub.Fingerprint()) {
		t.Fatalf("key not taken: %s", buf)
	}
	if _, err := OpenWithKey(dir, lg, &priv, "TNL_KEY"); err != nil {
		t.Fatalf("same key refused: %v", err)
	}
	other, _, _ := keys.Generate()
	_, err = OpenWithKey(dir, lg, &other, "TNL_KEY_FILE")
	if err == nil || err.Error() != "state.json holds a different Edge key than TNL_KEY_FILE" {
		t.Fatalf("err %v", err)
	}
	b, _ := other.MarshalText()
	c, _ := priv.MarshalText()
	if strings.Contains(err.Error(), string(b)) || strings.Contains(err.Error(), string(c)) {
		t.Fatal("the error carries a key")
	}
}
