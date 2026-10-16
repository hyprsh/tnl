//go:build !outpost

package main

import (
	"strings"
	"testing"
)

func TestRouteAddAndEditTakeTheOptionsAndLsShowsThem(t *testing.T) {
	runningEdge(t)
	tnl("zone", "add", "example.com")
	tnl("outpost", "add", "nas")
	tnl("outpost", "add", "pi")
	if _, code, err := tnl("route", "add", "a.example.com", "https://a", "--outpost", "nas",
		"--host-header", "a.lan", "--insecure-skip-verify"); err != nil || code != 0 {
		t.Fatal(err)
	}
	out, _, _ := tnl("route", "ls")
	if !strings.Contains(out, "host-header=a.lan insecure-skip-verify=true") {
		t.Fatalf("route ls:\n%s", out)
	}

	if _, _, err := tnl("route", "edit", "a.example.com", "--host-header", "", "--insecure-skip-verify=false",
		"--service", "http://b:8", "--outpost", "pi"); err != nil {
		t.Fatal(err)
	}
	out, _, _ = tnl("route", "ls")
	if !strings.Contains(out, "http://b:8") || !strings.Contains(out, "pi") || strings.Contains(out, "host-header") || strings.Contains(out, "insecure") {
		t.Fatalf("route ls after edit:\n%s", out)
	}

	for _, args := range [][]string{
		{"route", "edit", "a.example.com"},
		{"route", "edit"},
		{"route", "edit", "nope.example.com", "--host-header", "x"},
	} {
		if _, code, err := tnl(args...); err == nil || code != 1 {
			t.Errorf("%v: err=%v code=%d", args, err, code)
		}
	}
}
