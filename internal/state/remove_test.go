package state

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/declare"
)

func TestRemoveZoneSortsItsRoutesIntoRemovedWaitingAndMoved(t *testing.T) {
	st := declState(t)
	st.Zones = append(st.Zones, Zone{Name: "lab.example.com", Mode: PerHostname}, Zone{Name: "other.org", Mode: PerHostname})
	st.Routes = append(st.Routes,
		Route{Hostname: "a.lab.example.com", Outpost: "nas", Service: "http://localhost:1"},
		Route{Hostname: "a.other.org", Outpost: "nas", Service: "http://localhost:1"})
	st.ApplyDeclaration("newbox", []declare.Line{line("b.other.org", "http://localhost:3")}, time.Unix(100, 0))

	got := st.RemoveZone("other.org")
	if !reflect.DeepEqual(got, ZoneRemoval{Removed: []string{"a.other.org"}, Waiting: []string{"b.other.org"}}) {
		t.Fatalf("removal %+v", got)
	}
	if st.WaitingReason("newbox", "b.other.org") != "outside every Zone" {
		t.Fatalf("the declared line: %q", st.WaitingReason("newbox", "b.other.org"))
	}
	if err := st.Validate(); err != nil {
		t.Fatal(err)
	}

	// A parent Zone still contains the nested Zone's Routes: they stay.
	got = st.RemoveZone("lab.example.com")
	if !reflect.DeepEqual(got, ZoneRemoval{Moved: []string{"a.lab.example.com"}}) {
		t.Fatalf("removal %+v", got)
	}
	if _, ok := st.Route("a.lab.example.com"); !ok || len(st.Zones) != 1 {
		t.Fatalf("zones %+v routes %+v", st.Zones, st.Routes)
	}
}

func TestRemoveOutpostTakesItsRoutesAndDeclaration(t *testing.T) {
	st := declState(t)
	st.ApplyDeclaration("nas", []declare.Line{line("a.example.com", "http://localhost:2")}, time.Unix(100, 0))
	gone, hosts := st.RemoveOutpost("nas")
	slices.Sort(hosts)
	if gone.Name != "nas" || !reflect.DeepEqual(hosts, []string{"a.example.com"}) {
		t.Fatalf("gone %+v hosts %v", gone, hosts)
	}
	if st.Declares("nas") || len(st.Outposts) != 1 || len(st.Routes) != 0 {
		t.Fatalf("outposts %+v routes %+v declarations %+v", st.Outposts, st.Routes, st.Declarations)
	}
}
