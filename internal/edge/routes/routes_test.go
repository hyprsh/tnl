package routes

import (
	"net/netip"
	"testing"
)

func TestLookupFindsSwappedRoutesWithoutRestart(t *testing.T) {
	var tbl Mem
	if _, ok := tbl.Lookup("app.example.com"); ok {
		t.Fatal("empty table found a Route")
	}
	tbl.Swap([]Route{{Hostname: "app.example.com", Outpost: "home", Service: "localhost:3000"}})
	r, ok := tbl.Lookup("app.example.com")
	if !ok || r.Outpost != "home" || r.Service != "localhost:3000" {
		t.Fatalf("got %+v, %v", r, ok)
	}
	tbl.Swap(nil)
	if _, ok := tbl.Lookup("app.example.com"); ok {
		t.Fatal("Route survived a swap that dropped it")
	}
}

func TestLookupIgnoresCaseAndTrailingDot(t *testing.T) {
	var tbl Mem
	tbl.Swap([]Route{{Hostname: "App.Example.com", Outpost: "o", Service: "s:1"}})
	for _, h := range []string{"app.example.com", "APP.EXAMPLE.COM", "app.example.com."} {
		if _, ok := tbl.Lookup(h); !ok {
			t.Errorf("%q not found", h)
		}
	}
}

func TestMemOutposts(t *testing.T) {
	var o MemOutposts
	if _, ok := o.Addr("home"); ok {
		t.Fatal("empty set found an Outpost")
	}
	want := netip.MustParseAddrPort("100.64.0.2:7000")
	o.Set("home", want)
	if got, ok := o.Addr("home"); !ok || got != want {
		t.Fatalf("got %v, %v", got, ok)
	}
}
