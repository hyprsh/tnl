package state

import (
	"reflect"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/declare"
)

// waitingState has nas holding photos.example.com with a CLI Route and newbox
// declaring it, so newbox waits.
func waitingState(t *testing.T) *State {
	t.Helper()
	st := declState(t)
	st.Routes = []Route{{Hostname: "photos.example.com", Outpost: "nas", Service: "http://localhost:1"}}
	st.ApplyDeclaration("newbox", []declare.Line{line("photos.example.com", "http://localhost:9")}, time.Unix(100, 0))
	if st.WaitingReason("newbox", "photos.example.com") == "" {
		t.Fatal("newbox should wait")
	}
	return st
}

func TestHandoverGivesAFreedHostnameToTheWaitingLine(t *testing.T) {
	st := waitingState(t)
	st.RemoveRoutesVia("nas")
	got := st.Handover()
	want := []Handed{{Outpost: "newbox", Route: Route{Hostname: "photos.example.com", Outpost: "newbox", Service: "http://localhost:9"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if r, _ := st.Route("photos.example.com"); r.Outpost != "newbox" {
		t.Fatalf("Route %+v", r)
	}
	if err := st.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestHandoverLeavesAHeldHostnameAlone(t *testing.T) {
	st := waitingState(t)
	if got := st.Handover(); got != nil {
		t.Fatalf("handed %+v", got)
	}
}

func TestHandoverGivesTheHostnameToTheOutpostThatDeclaredItFirst(t *testing.T) {
	st := waitingState(t)
	// alpha sorts before newbox but declares the Hostname later.
	st.Outposts = append(st.Outposts, Outpost{Name: "alpha", PublicKey: mustKey(t), Address: st.AllocAddr()})
	st.ApplyDeclaration("alpha", []declare.Line{line("photos.example.com", "http://localhost:8")}, time.Unix(200, 0))
	st.RemoveRoutesVia("nas")
	got := st.Handover()
	if len(got) != 1 || got[0].Outpost != "newbox" {
		t.Fatalf("handed %+v", got)
	}
	if st.WaitingReason("alpha", "photos.example.com") == "" {
		t.Fatal("alpha should still wait")
	}
}

func TestHandoverAddsAWaitingLineWhenItsZoneAppears(t *testing.T) {
	st := declState(t)
	st.Routes = nil
	st.ApplyDeclaration("newbox", []declare.Line{line("a.example.org", "http://localhost:9")}, time.Unix(100, 0))
	if st.Handover() != nil {
		t.Fatal("handed with no Zone")
	}
	st.Zones = append(st.Zones, Zone{Name: "example.org", Mode: PerHostname})
	if got := st.Handover(); len(got) != 1 || got[0].Route.Hostname != "a.example.org" {
		t.Fatalf("handed %+v", got)
	}
}

func TestADeclaredRouteWhoseZoneGoesIsWaitingAgainAndTheStateStaysValid(t *testing.T) {
	st := declState(t)
	st.Routes = nil
	st.ApplyDeclaration("newbox", []declare.Line{line("a.example.com", "http://localhost:9")}, time.Unix(100, 0))
	st.Zones = nil
	st.RemoveRoutes(func(r Route) bool { return r.Hostname == "a.example.com" })
	if got := st.WaitingReason("newbox", "a.example.com"); got != "outside every Zone" {
		t.Fatalf("reason %q", got)
	}
	if err := st.Validate(); err != nil {
		t.Fatal(err)
	}
}
