package main

import (
	"strings"
	"testing"
)

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
