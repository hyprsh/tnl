package addr

import (
	"fmt"
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

func TestDeriveIsFrozen(t *testing.T) {
	// Worked out independently with Python's hashlib; these must never change.
	for name, want := range map[string]string{"nas": "100.85.233.86", "laptop": "100.69.25.55", "a": "100.90.148.130", "nas2": "100.74.166.40"} {
		if got := Derive(name); got.String() != want {
			t.Errorf("Derive(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDeriveStaysInRangeAndOffTheReservedAddresses(t *testing.T) {
	for i := 0; i < 20000; i++ {
		a := Derive(fmt.Sprintf("outpost-%d", i))
		if !Range.Contains(a) || a == Edge || a.String() == "100.64.0.0" || a.String() == "100.127.255.255" {
			t.Fatalf("outpost-%d derives %v", i, a)
		}
	}
}
