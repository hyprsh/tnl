package wire

import (
	"bytes"
	"errors"
	"testing"
)

func TestPingCarriesTheDeclarationDigest(t *testing.T) {
	d := Digest{1, 2, 3, 31: 9}
	b, err := AppendPing(nil, Ping{Version: "v1", Declaration: &d})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParsePing(b)
	if err != nil || got.Declaration == nil || *got.Declaration != d || got.Version != "v1" {
		t.Fatalf("got %+v, %v", got, err)
	}
	b, _ = AppendPing(nil, Ping{Version: "v1"})
	if got, err := ParsePing(b); err != nil || got.Declaration != nil {
		t.Fatalf("a ping without a digest: %+v, %v", got, err)
	}
}

func TestPongCarriesTheMatchFlagAndTheRoutesDigest(t *testing.T) {
	for _, matches := range []bool{true, false} {
		want := DeclarationAnswer{Matches: matches, Routes: Digest{7, 31: 1}}
		b, err := AppendPong(nil, Pong{Version: "v2", Declaration: &want})
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParsePong(b)
		if err != nil || got.Declaration == nil || *got.Declaration != want {
			t.Fatalf("matches=%v: got %+v, %v", matches, got, err)
		}
	}
}

func TestDeclarationFieldRefusals(t *testing.T) {
	head := []byte{'t', 'n', 'l', 1, 1, 1, 1, 'v'}
	short := append(append([]byte{}, head...), tagDigest, 3, 1, 2, 3, 0)
	if _, err := ParsePing(short); !errors.Is(err, ErrBadLength) {
		t.Errorf("short digest: %v", err)
	}
	long := append(append([]byte{}, head...), tagDigest, 33)
	long = append(append(long, bytes.Repeat([]byte{1}, 33)...), 0)
	if _, err := ParsePing(long); !errors.Is(err, ErrFieldTooLong) {
		t.Errorf("long digest: %v", err)
	}
	twice := append(append([]byte{}, head...), tagDigest, 32)
	twice = append(twice, bytes.Repeat([]byte{1}, 32)...)
	twice = append(twice, tagDigest, 32)
	twice = append(append(twice, bytes.Repeat([]byte{1}, 32)...), 0)
	if _, err := ParsePing(twice); !errors.Is(err, ErrDuplicate) {
		t.Errorf("repeated digest: %v", err)
	}
	// A pong carries neither the Outpost's digest tag nor half of its answer.
	pong := []byte{'t', 'n', 'l', 2, 1, 1, 1, 'v'}
	if _, err := ParsePong(append(append([]byte{}, pong...), tagDigest, 0, 0)); !errors.Is(err, ErrUnknownField) {
		t.Errorf("ping tag in a pong: %v", err)
	}
	if _, err := ParsePong(append(append([]byte{}, pong...), tagMatches, 1, 1, 0)); !errors.Is(err, ErrMissingField) {
		t.Errorf("flag without digest: %v", err)
	}
	bad := append(append([]byte{}, pong...), tagMatches, 1, 2, tagRoutes, 32)
	bad = append(append(bad, bytes.Repeat([]byte{1}, 32)...), 0)
	if _, err := ParsePong(bad); !errors.Is(err, ErrBadFlag) {
		t.Errorf("flag 2: %v", err)
	}
}
