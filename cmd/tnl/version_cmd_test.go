package main

import (
	"strings"
	"testing"
)

func TestVersionPrintsTheCompatibilityLine(t *testing.T) {
	t.Setenv("TNL_STATE_DIR", t.TempDir()+"/nothing-here")
	old := version
	t.Cleanup(func() { version = old })
	version = "0.3.1"
	out, _, err := tnl("version")
	if err != nil {
		t.Fatal(err)
	}
	if want := "tnl 0.3.1 " + buildName + "\nworks with Edges and Outposts 0.3.x\n"; out != want {
		t.Errorf("output %q, want %q", out, want)
	}
}

func TestVersionPrintsVersionAndBuildWithNoProcess(t *testing.T) {
	t.Setenv("TNL_STATE_DIR", t.TempDir()+"/nothing-here")
	out, code, err := tnl("version")
	if err != nil || code != 0 {
		t.Fatalf("exit %d, %v", code, err)
	}
	if want := version + " " + buildName; !strings.Contains(out, want) {
		t.Errorf("output %q lacks %q", out, want)
	}
	if buildName != "full" && buildName != "outpost" {
		t.Errorf("build is %q", buildName)
	}
}
