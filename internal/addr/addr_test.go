package addr

import (
	"net/netip"
	"testing"
)

func TestOutpostAddressesAreInRangeAndAfterTheEdge(t *testing.T) {
	for n, want := range map[int]string{0: "100.64.0.2", 1: "100.64.0.3", 253: "100.64.0.255", 254: "100.64.1.0"} {
		got := Outpost(n)
		if got.String() != want {
			t.Errorf("Outpost(%d) = %v, want %v", n, got, want)
		}
		if !Range.Contains(got) {
			t.Errorf("Outpost(%d) = %v is outside %v", n, got, Range)
		}
	}
	if Edge.String() != "100.64.0.1" || !Range.Contains(Edge) {
		t.Errorf("Edge = %v", Edge)
	}
}

func TestOutpostIndexInvertsOutpost(t *testing.T) {
	for _, n := range []int{0, 1, 253, 254, 70000} {
		if got, ok := OutpostIndex(Outpost(n)); !ok || got != n {
			t.Errorf("OutpostIndex(Outpost(%d)) = %d, %v", n, got, ok)
		}
	}
	for _, a := range []string{"100.64.0.1", "100.63.255.255", "10.0.0.2", "::1", "100.128.0.0"} {
		if n, ok := OutpostIndex(netip.MustParseAddr(a)); ok {
			t.Errorf("OutpostIndex(%s) = %d, want no index", a, n)
		}
	}
}
