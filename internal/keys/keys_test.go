package keys

import "testing"

func TestGenerateGivesDistinctPairs(t *testing.T) {
	p1, u1, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	p2, u2, _ := Generate()
	if p1 == p2 || u1 == u2 || p1 == u1 {
		t.Fatal("keys repeat")
	}
	if len(p1.Hex()) != 64 {
		t.Fatalf("hex length %d", len(p1.Hex()))
	}
}
