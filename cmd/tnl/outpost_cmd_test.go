//go:build !outpost

package main

import (
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/token"
)

func TestOutpostAddPrintsTheTokenAlone(t *testing.T) {
	runningEdge(t)
	out, err, code := tnl("outpost", "add", "laptop")
	if err != nil || code != 0 {
		t.Fatalf("%v %d", err, code)
	}
	tok, err := token.Decode(out)
	if err != nil {
		t.Fatalf("stdout %q is not a token: %v", out, err)
	}
	if strings.Count(out, "\n") != 1 || tok.TunnelAddr.String() != "100.64.0.2" {
		t.Fatalf("stdout %q, token %+v", out, tok)
	}
}

func TestOutpostAddRefusalsExitOne(t *testing.T) {
	runningEdge(t)
	if _, err, _ := tnl("outpost", "add", "laptop"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"outpost", "add", "laptop"}, {"outpost", "add", "Bad_Name"}, {"outpost", "add"}} {
		if out, err, code := tnl(args...); err == nil || code != 1 || out != "" {
			t.Errorf("%v: out=%q err=%v code=%d", args, out, err, code)
		}
	}
}
