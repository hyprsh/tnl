//go:build outpost

package main

import (
	"strings"
	"testing"
)

func TestOutpostBuildRefusesEveryEdgeCommand(t *testing.T) {
	for _, args := range [][]string{{"edge"}, {"outpost", "add", "x"}, {"zone", "add", "example.com"}, {"route", "add", "a.example.com"}} {
		out, code, err := tnl(args...)
		if code != 1 || err == nil || out != "" {
			t.Errorf("%v: exit %d, %v, printed %q", args, code, err, out)
			continue
		}
		if !strings.Contains(err.Error(), "this is the Outpost build") || !strings.Contains(err.Error(), "full build") {
			t.Errorf("%v: %q", args, err)
		}
	}
}

func TestOutpostBuildNamesItselfOutpost(t *testing.T) {
	if buildName != "outpost" {
		t.Fatalf("build is %q", buildName)
	}
}
