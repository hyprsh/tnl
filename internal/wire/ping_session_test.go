package wire

import (
	"bytes"
	"errors"
	"testing"
)

func TestPingCarriesTheSessionIdAndItIsOptional(t *testing.T) {
	s := Session{1, 2, 15: 9}
	b, err := AppendPing(nil, Ping{Version: "v1", Session: &s})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParsePing(b)
	if err != nil || got.Session == nil || *got.Session != s {
		t.Fatalf("got %+v, %v", got, err)
	}
	b, _ = AppendPing(nil, Ping{Version: "v1"})
	if got, err := ParsePing(b); err != nil || got.Session != nil {
		t.Fatalf("a ping without a session id: %+v, %v", got, err)
	}
}

func TestSessionFieldRefusals(t *testing.T) {
	head := []byte{'t', 'n', 'l', 1, 1, 1, 1, 'v'}
	short := append(append([]byte{}, head...), tagSession, 3, 1, 2, 3, 0)
	if _, err := ParsePing(short); !errors.Is(err, ErrBadLength) {
		t.Errorf("short id: %v", err)
	}
	long := append(append([]byte{}, head...), tagSession, 17)
	long = append(append(long, bytes.Repeat([]byte{1}, 17)...), 0)
	if _, err := ParsePing(long); !errors.Is(err, ErrFieldTooLong) {
		t.Errorf("long id: %v", err)
	}
	twice := append(append([]byte{}, head...), tagSession, 16)
	twice = append(twice, bytes.Repeat([]byte{1}, 16)...)
	twice = append(twice, tagSession, 16)
	twice = append(append(twice, bytes.Repeat([]byte{1}, 16)...), 0)
	if _, err := ParsePing(twice); !errors.Is(err, ErrDuplicate) {
		t.Errorf("repeated id: %v", err)
	}
	pong := []byte{'t', 'n', 'l', 2, 1, 1, 1, 'v'}
	if _, err := ParsePong(append(append([]byte{}, pong...), tagSession, 0, 0)); !errors.Is(err, ErrUnknownField) {
		t.Errorf("session tag in a pong: %v", err)
	}
}
