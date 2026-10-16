//go:build !outpost

package main

import (
	"strings"
	"testing"
)

func TestRouteRmRemovesTheRouteAndUnknownExitsOne(t *testing.T) {
	runningEdge(t)
	tnl("zone", "add", "example.com")
	tnl("outpost", "add", "nas")
	tnl("route", "add", "a.example.com", "http://a:1", "--outpost", "nas")
	tnl("route", "add", "b.example.com", "http://b:1", "--outpost", "nas")
	out, code, err := tnl("route", "rm", "a.example.com")
	if err != nil || code != 0 || !strings.Contains(out, "a.example.com") {
		t.Fatalf("%q %v %d", out, err, code)
	}
	out, _, _ = tnl("route", "ls")
	if strings.Contains(out, "a.example.com") || !strings.Contains(out, "b.example.com") {
		t.Fatalf("route ls:\n%s", out)
	}
	for _, args := range [][]string{
		{"route", "rm", "a.example.com"},
		{"route", "rm"},
		{"route", "rm", "a", "b"},
	} {
		_, code, err := tnl(args...)
		if err == nil || code != 1 {
			t.Errorf("%v: err=%v code=%d", args, err, code)
		}
	}
}
