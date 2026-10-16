//go:build outpost

package main

import (
	"strings"
	"testing"
)

func TestOutpostBuildRefusesEveryEdgeCommand(t *testing.T) {
	for _, args := range [][]string{{"edge"}, {"outpost", "add", "x"}, {"zone", "add", "example.com"}, {"zone", "rm", "example.com"}} {
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

func TestOutpostBuildRunsRouteAndZoneLs(t *testing.T) {
	t.Setenv("TNL_STATE_DIR", t.TempDir())
	for _, args := range [][]string{{"route", "add", "a.example.com", "http://x:1"}, {"route", "edit", "a.example.com"}, {"route", "rm", "a.example.com"}, {"route", "ls"}, {"zone", "ls"}} {
		_, _, err := tnl(args...)
		if err != nil && strings.Contains(err.Error(), "this is the Outpost build") {
			t.Errorf("%v is refused: %v", args, err)
		}
	}
}
