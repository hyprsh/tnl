package wire

import (
	"bytes"
	"errors"
	"testing"
)

func TestPongCarriesTheWindowFlag(t *testing.T) {
	for _, inside := range []bool{true, false} {
		b, err := AppendPong(nil, Pong{Version: "0.3.4", Inside: inside})
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParsePong(b)
		if err != nil || got.Version != "0.3.4" || got.Inside != inside {
			t.Fatalf("inside=%v: got %+v, %v", inside, got, err)
		}
	}
}

func TestPongWithoutAFlagIsInside(t *testing.T) {
	got, err := ParsePong([]byte{'t', 'n', 'l', 2, 1, 1, 1, 'x', 0})
	if err != nil || !got.Inside {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestPongRefusesABadFlag(t *testing.T) {
	for name, in := range map[string][]byte{
		"value 2":  {'t', 'n', 'l', 2, 1, 1, 1, 'x', 2, 1, 2, 0},
		"too long": {'t', 'n', 'l', 2, 1, 1, 1, 'x', 2, 2, 1, 1, 0},
		"repeated": {'t', 'n', 'l', 2, 1, 1, 1, 'x', 2, 1, 1, 2, 1, 1, 0},
	} {
		if _, err := ParsePong(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The window flag is the answer's: a ping that carries it is refused.
func TestPingRefusesTheWindowFlag(t *testing.T) {
	if _, err := ParsePing([]byte{'t', 'n', 'l', 1, 1, 1, 1, 'x', 2, 1, 1, 0}); !errors.Is(err, ErrUnknownField) {
		t.Fatalf("err = %v", err)
	}
}

func TestHeaderFormatIsPickedFromTheOutpostsVersion(t *testing.T) {
	for _, v := range []string{"0.3.1", "1.4.2", "2.0.0", "dev", ""} {
		if got := FormatFor(v); got != FormatV1 {
			t.Errorf("FormatFor(%q) = %d, want %d", v, got, FormatV1)
		}
	}
}

func TestWriteHeaderFormatSendsOnlyTheFormatsFields(t *testing.T) {
	h := Header{Service: "a:1", Hostname: "h", RequestID: "r"}
	var got, want bytes.Buffer
	if err := WriteHeaderFormat(&got, h, FormatV1); err != nil {
		t.Fatal(err)
	}
	if err := WriteHeader(&want, h); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want.Bytes()) {
		t.Fatalf("format 1 bytes differ: %v vs %v", got.Bytes(), want.Bytes())
	}
	if err := WriteHeaderFormat(&got, Header{Service: "a:1"}, 2); err == nil {
		t.Fatal("wrote a format that doesn't exist")
	}
}
