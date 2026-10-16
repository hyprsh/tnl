//go:build !outpost

package main

import (
	"strings"
	"testing"
)

func TestZoneEditSwitchesModesAndReadsTheTokenFromStdin(t *testing.T) {
	g := new(gotToken)
	edgeWildcards = g
	t.Cleanup(func() { edgeWildcards = nil })
	runningEdge(t)
	tnl("zone", "add", "example.com")

	withStdin(t, "from-stdin\n")
	out, errOut, code, err := tnlBoth("zone", "edit", "example.com", "--wildcard", "cloudflare")
	if err != nil || code != 0 {
		t.Fatalf("%q %q %v", out, errOut, err)
	}
	if g.token != "from-stdin" || g.provider != "cloudflare" || g.zone != "example.com" {
		t.Errorf("the Edge got %q %q %q", g.provider, g.token, g.zone)
	}
	for _, want := range []string{"DNS zone example.com", "Zone example.com is now wildcard", "*.example.com, example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q: %q", want, out)
		}
	}
	if ls, _, _ := tnl("zone", "ls"); !strings.Contains(ls, "wildcard") || strings.Contains(ls, "from-stdin") {
		t.Errorf("%q", ls)
	}

	withStdin(t, "replacement\n")
	out, _, code, err = tnlBoth("zone", "edit", "example.com", "--token")
	if err != nil || code != 0 || g.token != "replacement" || !strings.Contains(out, "replaced the token") {
		t.Fatalf("--token: %q %d %v (the Edge got %q)", out, code, err, g.token)
	}

	out, _, code, err = tnlBoth("zone", "edit", "example.com", "--per-hostname")
	if err != nil || code != 0 || !strings.Contains(out, "per-hostname") {
		t.Fatalf("--per-hostname: %q %d %v", out, code, err)
	}
	if ls, _, _ := tnl("zone", "ls"); strings.Contains(ls, "wildcard") || strings.Contains(ls, "cloudflare") {
		t.Errorf("%q", ls)
	}
}

func TestZoneEditTakesExactlyOneChange(t *testing.T) {
	edgeWildcards = new(gotToken)
	t.Cleanup(func() { edgeWildcards = nil })
	runningEdge(t)
	tnl("zone", "add", "example.com")
	for _, args := range [][]string{
		{"zone", "edit", "example.com"},
		{"zone", "edit", "example.com", "--per-hostname", "--token"},
		{"zone", "edit", "example.com", "--wildcard", "cloudflare", "--per-hostname"},
		{"zone", "edit"},
		{"zone", "edit", "a.com", "b.com", "--per-hostname"},
	} {
		withStdin(t, "x\n")
		if _, _, err := tnl(args...); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%v: %v", args, err)
		}
	}
	withStdin(t, "x\n")
	if _, _, err := tnl("zone", "edit", "example.com", "--wildcard", "cloudflare", "--token", "x"); err == nil {
		t.Error("--token took a value")
	}
}
