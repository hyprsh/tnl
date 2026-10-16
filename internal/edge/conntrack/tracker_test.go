package conntrack

import (
	"net"
	"testing"
	"time"
)

// open returns a tracked client end and a func that reports whether the server
// end saw the connection close.
func open(t *testing.T, tr *Tracker, hostname, outpost string) (client net.Conn, closed func() bool) {
	t.Helper()
	c, s := net.Pipe()
	t.Cleanup(func() { c.Close(); s.Close() })
	tracked := tr.Track(hostname, outpost, c)
	done := make(chan struct{})
	go func() { s.Read(make([]byte, 1)); close(done) }()
	return tracked, func() bool {
		select {
		case <-done:
			return true
		case <-time.After(20 * time.Millisecond):
			return false
		}
	}
}

func TestCloseRouteClosesOnlyThatRoutesConnections(t *testing.T) {
	tr := New()
	_, a1 := open(t, tr, "a.example.com", "laptop")
	_, a2 := open(t, tr, "A.example.com", "laptop")
	_, b := open(t, tr, "b.example.com", "laptop")
	tr.CloseRoute("a.example.com")
	if !a1() || !a2() {
		t.Fatal("a Route's connections stayed open")
	}
	if b() {
		t.Fatal("another Route's connection was closed")
	}
}

func TestCloseOutpostClosesAllOfItsConnections(t *testing.T) {
	tr := New()
	_, a := open(t, tr, "a.example.com", "laptop")
	_, b := open(t, tr, "b.example.com", "laptop")
	_, c := open(t, tr, "c.example.com", "server")
	tr.CloseOutpost("laptop")
	if !a() || !b() {
		t.Fatal("the Outpost's connections stayed open")
	}
	if c() {
		t.Fatal("another Outpost's connection was closed")
	}
}

func TestCloseAllClosesEverything(t *testing.T) {
	tr := New()
	_, a := open(t, tr, "a.example.com", "laptop")
	_, c := open(t, tr, "c.example.com", "server")
	tr.CloseAll()
	if !a() || !c() {
		t.Fatal("connections stayed open")
	}
}

func TestClosedConnectionsAreForgotten(t *testing.T) {
	tr := New()
	c, _ := open(t, tr, "a.example.com", "laptop")
	if tr.Count() != 1 {
		t.Fatalf("count = %d, want 1", tr.Count())
	}
	c.Close()
	c.Close() // idempotent
	if tr.Count() != 0 {
		t.Fatalf("count = %d after close, want 0", tr.Count())
	}
}

func TestNilTrackerTracksNothing(t *testing.T) {
	var tr *Tracker
	c, _ := net.Pipe()
	if tr.Track("a.example.com", "laptop", c) != c {
		t.Fatal("a nil Tracker wrapped the connection")
	}
	tr.CloseRoute("a.example.com")
	tr.CloseOutpost("laptop")
	tr.CloseAll()
}
