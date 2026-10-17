//go:build !outpost

package main

import (
	"strings"
	"testing"
)

func TestCompleteValuesReadsNamesFromTheRunningEdge(t *testing.T) {
	runningEdge(t)
	tnl("zone", "add", "example.com")
	tnl("outpost", "add", "nas")
	tnl("route", "add", "app.example.com", "http://localhost:3000", "--outpost", "nas")
	for kind, want := range map[string]string{"outposts": "nas", "routes": "app.example.com", "zones": "example.com"} {
		out, code, err := tnl("__complete", kind)
		if err != nil || code != 0 || strings.TrimSpace(out) != want {
			t.Errorf("%s: %q %d %v", kind, out, code, err)
		}
	}
}

func TestCompleteValuesPrintsNothingWhenNoProcessAnswers(t *testing.T) {
	t.Setenv("TNL_STATE_DIR", t.TempDir())
	out, code, err := tnl("__complete", "outposts")
	if out != "" || code != 0 || err != nil {
		t.Errorf("%q %d %v", out, code, err)
	}
}
