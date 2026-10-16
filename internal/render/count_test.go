package render

import "testing"

func TestCountUsesTheNounInItsNumber(t *testing.T) {
	for n, want := range map[int]string{0: "0 Routes", 1: "1 Route", 2: "2 Routes"} {
		if got := Count(n, "Route", "Routes"); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
}
