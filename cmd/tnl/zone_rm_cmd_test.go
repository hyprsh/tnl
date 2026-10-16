//go:build !outpost

package main

import (
	"strings"
	"testing"
)

func TestZoneRmRefusesWhileRoutesAreStrandedThenRemovesThemWithTheFlag(t *testing.T) {
	runningEdge(t)
	tnl("zone", "add", "example.com")
	tnl("zone", "add", "lab.example.com")
	tnl("outpost", "add", "nas")
	tnl("route", "add", "www.example.com", "http://a:1", "--outpost", "nas")
	tnl("route", "add", "app.lab.example.com", "http://a:1", "--outpost", "nas")

	out, code, err := tnl("zone", "rm", "lab.example.com")
	if err != nil || code != 0 || !strings.Contains(out, "app.lab.example.com") || !strings.Contains(out, "example.com") {
		t.Fatalf("moving: %q %d %v", out, code, err)
	}
	_, code, err = tnl("zone", "rm", "example.com")
	if err == nil || code != 1 || !strings.Contains(err.Error(), "www.example.com") || !strings.Contains(err.Error(), "app.lab.example.com") {
		t.Fatalf("refusal: %d %v", code, err)
	}
	out, code, err = tnl("zone", "rm", "example.com", "--with-routes")
	if err != nil || code != 0 || !strings.Contains(out, "www.example.com") {
		t.Fatalf("with routes: %q %d %v", out, code, err)
	}
	if out, _, _ := tnl("route", "ls"); strings.Contains(out, "example.com") {
		t.Errorf("Routes left: %q", out)
	}
}
