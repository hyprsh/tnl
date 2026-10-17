package liveness

import (
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/wire"
)

func newSessionRig() (r *rig, closed *[]string) {
	r = newRig()
	closed = new([]string)
	r.Liveness.cfg.OnRestart = func(name string) { *closed = append(*closed, name) }
	return r, closed
}

func TestTheFirstSessionIdAfterTheEdgeStartsClosesNothing(t *testing.T) {
	r, closed := newSessionRig()
	r.Heard("home")
	r.SetSession("home", wire.Session{1})
	if len(*closed) != 0 || strings.Contains(r.log.String(), "restarted") {
		t.Fatalf("closed %v\n%s", *closed, r.log)
	}
}

func TestTheSameSessionIdAgainClosesNothing(t *testing.T) {
	r, closed := newSessionRig()
	r.Heard("home")
	r.SetSession("home", wire.Session{1})
	r.SetSession("home", wire.Session{1})
	if len(*closed) != 0 {
		t.Fatalf("closed %v", *closed)
	}
}

func TestAChangedSessionIdClosesTheOutpostsConnectionsOnceWithOneInfoLine(t *testing.T) {
	r, closed := newSessionRig()
	r.Heard("home")
	r.SetSession("home", wire.Session{1})
	r.SetSession("home", wire.Session{2})
	r.SetSession("home", wire.Session{2})
	if len(*closed) != 1 || (*closed)[0] != "home" {
		t.Fatalf("closed %v", *closed)
	}
	if n := strings.Count(r.log.String(), "outpost restarted, closing its connections"); n != 1 {
		t.Fatalf("%d info lines:\n%s", n, r.log)
	}
	if r.State("home") != Online {
		t.Fatal("the Outpost's state changed")
	}
}

func TestAnUntrackedOutpostRecordsNothing(t *testing.T) {
	r, closed := newSessionRig()
	r.SetSession("home", wire.Session{1})
	r.SetSession("home", wire.Session{2})
	if len(*closed) != 0 {
		t.Fatalf("closed %v", *closed)
	}
}
