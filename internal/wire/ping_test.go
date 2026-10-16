package wire

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPingRoundTrip(t *testing.T) {
	b, err := AppendPing(nil, Ping{Version: "v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParsePing(b)
	if err != nil || got.Version != "v1.2.3" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestPongRoundTrip(t *testing.T) {
	b, err := AppendPong(nil, Pong{Version: "v9"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParsePong(b)
	if err != nil || got.Version != "v9" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestPingStartsWithMarkerAndFormat(t *testing.T) {
	b, _ := AppendPing(nil, Ping{Version: "x"})
	if want := []byte{'t', 'n', 'l', 1, 1}; !bytes.Equal(b[:5], want) {
		t.Fatalf("first bytes %v, want %v", b[:5], want)
	}
	b, _ = AppendPong(nil, Pong{Version: "x"})
	if want := []byte{'t', 'n', 'l', 2, 1}; !bytes.Equal(b[:5], want) {
		t.Fatalf("first bytes %v, want %v", b[:5], want)
	}
}

func TestPingIsNotAPongNorAHeader(t *testing.T) {
	ping, _ := AppendPing(nil, Ping{Version: "x"})
	if _, err := ParsePong(ping); !errors.Is(err, ErrBadMarker) {
		t.Fatalf("pong parse of a ping: %v", err)
	}
	if _, err := ReadHeader(bytes.NewReader(ping)); !errors.Is(err, ErrBadMarker) {
		t.Fatalf("header parse of a ping: %v", err)
	}
}

func TestParsePingRefusals(t *testing.T) {
	good, _ := AppendPing(nil, Ping{Version: "v1"})
	cases := map[string]struct {
		in   []byte
		want error
	}{
		"bad marker":     {[]byte("GET / HTTP/1.1\r\n\r\n"), ErrBadMarker},
		"unknown format": {append(append([]byte{}, good[:4]...), 9, 0), ErrUnknownFormat},
		"unknown field":  {[]byte{'t', 'n', 'l', 1, 1, 77, 0, 0}, ErrUnknownField},
		"repeated":       {[]byte{'t', 'n', 'l', 1, 1, 1, 1, 'a', 1, 1, 'b', 0}, ErrDuplicate},
		"too long":       {append([]byte{'t', 'n', 'l', 1, 1, 1, 200, 1}, bytes.Repeat([]byte{'a'}, 200)...), ErrFieldTooLong},
		"no version":     {[]byte{'t', 'n', 'l', 1, 1, 0}, ErrMissingField},
		"truncated":      {good[:len(good)-2], ErrTruncated},
		"no end":         {good[:len(good)-1], ErrTruncated},
		"trailing bytes": {append(append([]byte{}, good...), 0), ErrTrailing},
		"empty":          {nil, ErrTruncated},
	}
	for name, c := range cases {
		if _, err := ParsePing(c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
}

func TestAppendPingRefusesLongVersion(t *testing.T) {
	if _, err := AppendPing(nil, Ping{Version: strings.Repeat("a", maxVersion+1)}); !errors.Is(err, ErrFieldTooLong) {
		t.Fatalf("err = %v", err)
	}
}
