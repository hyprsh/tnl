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

func TestTextRoundTripAndPublic(t *testing.T) {
	priv, pub, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	txt, _ := priv.MarshalText()
	if len(txt) != 44 {
		t.Fatalf("want 44 base64 chars, got %d", len(txt))
	}
	var back Key
	if err := back.UnmarshalText(txt); err != nil || back != priv {
		t.Fatalf("round trip: %v", err)
	}
	if priv.Public() != pub {
		t.Fatal("Public() disagrees with Generate")
	}
	if err := back.UnmarshalText([]byte("short")); err == nil {
		t.Fatal("a short key parsed")
	}
}

func TestFingerprintFormat(t *testing.T) {
	// SHA-256 of 32 zero bytes, unpadded base64, after "SHA256:".
	got := Key{}.Fingerprint()
	want := "SHA256:Zmh6rfhivXdsj8GLjp+OIAiXFIVu4jOzkCpZHQ1fKSU"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
