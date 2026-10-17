package edge

import (
	"testing"

	"github.com/hyprsh/tnl/internal/edge/liveness"
)

// A later Outpost's ping has a tag this Edge refuses. Its version still decides.
func TestRefusedPingIsJudgedByItsVersion(t *testing.T) {
	l := liveness.New(liveness.Config{EdgeVersion: "0.2.0"})
	ping := func(version string) []byte {
		b := []byte{'t', 'n', 'l', 1, 1, 1, byte(len(version))}
		b = append(b, version...)
		return append(b, 99, 1, 7, 0) // an unknown tag, then the end
	}
	if p, ok := judgeRefusedPing(l, ping("0.3.0")); !ok || p.Version != "0.3.0" {
		t.Errorf("a newer minor: %+v, %v", p, ok)
	}
	if _, ok := judgeRefusedPing(l, ping("0.2.5")); ok {
		t.Error("a ping inside the window was taken")
	}
	if _, ok := judgeRefusedPing(l, []byte{'t', 'n', 'l', 1, 9, 0}); ok {
		t.Error("a ping of an unknown format was taken")
	}
}
