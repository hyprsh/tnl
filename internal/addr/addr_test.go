package addr

import "testing"

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
