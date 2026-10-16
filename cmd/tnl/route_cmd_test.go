//go:build !outpost

package main

import (
	"strings"
	"testing"
)

func TestRouteAddThenLs(t *testing.T) {
	runningEdge(t)
	tnl("zone", "add", "example.com")
	if _, _, err := tnl("outpost", "add", "nas"); err != nil {
		t.Fatal(err)
	}
	out, code, err := tnl("route", "add", "app.example.com", "http://localhost:3000", "--outpost", "nas")
	if err != nil || code != 0 || !strings.Contains(out, "app.example.com") {
		t.Fatalf("%q %v %d", out, err, code)
	}
	if _, _, err := tnl("route", "add", "--outpost", "nas", "a.example.com", "http://nas"); err != nil {
		t.Fatal(err)
	}
	out, _, err = tnl("route", "ls")
	if err != nil {
		t.Fatal(err)
	}
	want := "HOSTNAME         OUTPOST  SERVICE                OPTIONS\n" +
		"a.example.com    nas      http://nas             -\n" +
		"app.example.com  nas      http://localhost:3000  -\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestRouteAddNeedsOutpostAndRefusalsExitOne(t *testing.T) {
	runningEdge(t)
	tnl("zone", "add", "example.com")
	tnl("outpost", "add", "nas")
	for _, args := range [][]string{
		{"route", "add", "app.example.com", "http://x:1"},
		{"route", "add", "app.example.com", "http://x:1", "--outpost", "ghost"},
		{"route", "add", "app.example.com", "ftp://x:1", "--outpost", "nas"},
		{"route", "add", "app.example.com", "--outpost", "nas"},
		{"route", "ls", "extra"},
	} {
		_, code, err := tnl(args...)
		if err == nil || code != 1 {
			t.Errorf("%v: err=%v code=%d", args, err, code)
		}
	}
}
