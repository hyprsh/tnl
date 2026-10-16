package state

import (
	"slices"
	"testing"
)

func nested() *State {
	return &State{
		Zones: []Zone{{Name: "example.com"}, {Name: "lab.example.com"}},
		Routes: []Route{
			{Hostname: "app.lab.example.com", Outpost: "a"},
			{Hostname: "www.example.com", Outpost: "a"},
			{Hostname: "lab.example.com", Outpost: "b"},
		},
	}
}

func TestAHostnameBelongsToTheLongestZone(t *testing.T) {
	st := nested()
	for host, want := range map[string]string{
		"app.lab.example.com": "lab.example.com",
		"lab.example.com":     "lab.example.com",
		"www.example.com":     "example.com",
		"example.com":         "example.com",
	} {
		z, ok := st.ZoneOf(host)
		if !ok || z.Name != want {
			t.Errorf("ZoneOf(%s) = %q, %v; want %q", host, z.Name, ok, want)
		}
	}
	if _, ok := st.ZoneOf("evilexample.com"); ok || st.InZone("evilexample.com") {
		t.Error("evilexample.com is not in example.com")
	}
}

func TestRoutesStrandedByAZoneAreThoseNoOtherZoneContains(t *testing.T) {
	st := nested()
	if got := st.RoutesStrandedBy("lab.example.com"); len(got) != 0 {
		t.Errorf("the parent holds them, stranded: %v", got)
	}
	// example.com's own Route is stranded; the nested Zone still holds the others.
	if got := st.RoutesStrandedBy("example.com"); !slices.Equal(got, []string{"www.example.com"}) {
		t.Errorf("stranded %v", got)
	}
}

func TestRemoveRoutesReturnsAndDropsTheMatching(t *testing.T) {
	st := nested()
	got := st.RemoveRoutes(func(r Route) bool { return r.Outpost == "a" })
	if !slices.Equal(got, []string{"app.lab.example.com", "www.example.com"}) || len(st.Routes) != 1 {
		t.Errorf("removed %v, left %v", got, st.Routes)
	}
}
