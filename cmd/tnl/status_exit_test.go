package main

import "testing"

// These hold in both builds: exit 2 needs no process and no Edge code.

func TestStatusExitsTwoWithNoRunningProcess(t *testing.T) {
	candidates(t, emptyDir(t), emptyDir(t))
	out, code, err := tnl("status")
	if code != 2 || err == nil || out != "" {
		t.Fatalf("%q %d %v", out, code, err)
	}
	// And with a state directory pinned to one with no socket.
	t.Setenv("TNL_STATE_DIR", emptyDir(t))
	if _, code, err := tnl("status", "--json"); code != 2 || err == nil {
		t.Fatalf("pinned: %d %v", code, err)
	}
}

func TestStatusExitsTwoWhenSeveralProcessesAreRunning(t *testing.T) {
	candidates(t, socketDir(t), socketDir(t))
	if _, code, _ := tnl("status"); code != 2 {
		t.Fatalf("code %d", code)
	}
}
