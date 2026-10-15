package harness

import (
	"io"
	"testing"

	"github.com/hyprsh/tnl/internal/wire"
)

// The slice-1 join, end to end: `outpost add` on the running Edge, then the
// Outpost connects with the token, and the Edge reaches a Service through it.
func TestEdgeReachesAServiceThroughAnOutpostThatJoinedWithAToken(t *testing.T) {
	svc := NewEchoService(t)
	re := StartEdge(t)
	j := re.JoinOutpost(t, "laptop")

	if j.Addr.String() != "100.64.0.2" {
		t.Errorf("first Outpost got %s", j.Addr)
	}
	conn, err := re.Dial(ctx(t), j.Outpost, wire.Header{Service: svc.Addr(), Hostname: "app.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("through the joined outpost")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("through the joined outpost"))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "through the joined outpost" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestSecondOutpostGetsTheNextAddressAndAlsoWorks(t *testing.T) {
	svc := NewEchoService(t)
	re := StartEdge(t)
	re.JoinOutpost(t, "laptop")
	j := re.JoinOutpost(t, "nas")
	if j.Addr.String() != "100.64.0.3" {
		t.Errorf("second Outpost got %s", j.Addr)
	}
	conn, err := re.Dial(ctx(t), j.Outpost, wire.Header{Service: svc.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}
