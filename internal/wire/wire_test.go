package wire

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestHeaderRoundTrip(t *testing.T) {
	want := Header{Service: "10.0.0.5:8080", Hostname: "app.example.com", RequestID: "req-0123456789abcdef"}
	var buf bytes.Buffer
	if err := WriteHeader(&buf, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadHeader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestHeaderStartsWithMarkerAndFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteHeader(&buf, Header{Service: "a:1"}); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.Bytes()[:5], []byte{'t', 'n', 'l', 0, 1}; !bytes.Equal(got, want) {
		t.Fatalf("first bytes %v, want %v", got, want)
	}
}

func TestReadHeaderRefusesUnknownMarker(t *testing.T) {
	_, err := ReadHeader(strings.NewReader("GET / HTTP/1.1\r\n\r\n"))
	if !errors.Is(err, ErrBadMarker) {
		t.Fatalf("err = %v, want ErrBadMarker", err)
	}
}

func TestReadHeaderRefusesUnknownFormat(t *testing.T) {
	_, err := ReadHeader(bytes.NewReader([]byte{'t', 'n', 'l', 0, 9, 0}))
	if !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("err = %v, want ErrUnknownFormat", err)
	}
}

func TestReadHeaderRefusesUnknownField(t *testing.T) {
	// marker, format 1, service "a:1", then tag 99 with 1 byte, then end.
	raw := []byte{'t', 'n', 'l', 0, 1, 1, 3, 'a', ':', '1', 99, 1, 'x', 0}
	_, err := ReadHeader(bytes.NewReader(raw))
	if !errors.Is(err, ErrUnknownField) {
		t.Fatalf("err = %v, want ErrUnknownField", err)
	}
}

func TestReadHeaderRefusesMissingService(t *testing.T) {
	raw := []byte{'t', 'n', 'l', 0, 1, 0}
	if _, err := ReadHeader(bytes.NewReader(raw)); err == nil {
		t.Fatal("want an error for a header without a Service")
	}
}

func TestReadHeaderRefusesOversizedField(t *testing.T) {
	raw := []byte{'t', 'n', 'l', 0, 1, 1}
	raw = append(raw, 0xff, 0xff, 0x03) // uvarint 65535
	if _, err := ReadHeader(bytes.NewReader(raw)); !errors.Is(err, ErrFieldTooLong) {
		t.Fatalf("err = %v, want ErrFieldTooLong", err)
	}
}

func TestReplyRoundTrip(t *testing.T) {
	for _, want := range []Reply{
		{Status: StatusOK},
		{Status: StatusDialError, Reason: ReasonRefused},
		{Status: StatusDialError, Reason: ReasonTimeout},
		{Status: StatusDialError, Reason: ReasonDNS},
		{Status: StatusDialError, Reason: ReasonAllowlist},
		{Status: StatusUnsupported, Version: "0.1.0"},
	} {
		var buf bytes.Buffer
		if err := WriteReply(&buf, want); err != nil {
			t.Fatal(err)
		}
		got, err := ReadReply(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
}

func TestUnsupportedReplyBytesAreFixed(t *testing.T) {
	// This reply never changes across majors: status 2, length, version.
	var buf bytes.Buffer
	if err := WriteReply(&buf, Reply{Status: StatusUnsupported, Version: "1.2.3"}); err != nil {
		t.Fatal(err)
	}
	want := []byte{2, 5, '1', '.', '2', '.', '3'}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got %v, want %v", buf.Bytes(), want)
	}
}

func TestReadReplyRefusesUnknownStatusAndReason(t *testing.T) {
	if _, err := ReadReply(bytes.NewReader([]byte{7})); err == nil {
		t.Fatal("want an error for an unknown status")
	}
	if _, err := ReadReply(bytes.NewReader([]byte{1, 9})); err == nil {
		t.Fatal("want an error for an unknown reason")
	}
}
