package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// PingPort is the UDP port of the ping, on the Edge's Tunnel address and on
// each Outpost's. It exists only inside the netstacks (the API is 7001, the
// data port 7000).
const PingPort = 7002

// PingMarker starts the Outpost's ping and PongMarker the Edge's answer. They
// differ from Marker and from each other, so no message is taken for another.
// The format number follows, then fields as in the header.
var (
	PingMarker = [4]byte{'t', 'n', 'l', 1}
	PongMarker = [4]byte{'t', 'n', 'l', 2}
)

// Ping and pong field tag. Later slices add the window flag and the
// Declaration digests as new tags.
const tagPingVersion = 1

var (
	ErrTruncated = errors.New("tnl: ping is cut short")
	ErrTrailing  = errors.New("tnl: ping has bytes after its end")
)

// Ping is what an Outpost sends the Edge every 10 s.
type Ping struct {
	Version string // the Outpost's version
}

// Pong is the Edge's answer.
type Pong struct {
	Version string // the Edge's version
}

// AppendPing appends the datagram for p to b.
func AppendPing(b []byte, p Ping) ([]byte, error) { return appendVersioned(b, PingMarker, p.Version) }

// AppendPong appends the datagram for p to b.
func AppendPong(b []byte, p Pong) ([]byte, error) { return appendVersioned(b, PongMarker, p.Version) }

// ParsePing reads a whole datagram, refusing an unknown marker, format number
// or field.
func ParsePing(b []byte) (Ping, error) {
	v, err := parseVersioned(b, PingMarker)
	return Ping{Version: v}, err
}

// ParsePong reads a whole datagram, refusing what ParsePing refuses.
func ParsePong(b []byte) (Pong, error) {
	v, err := parseVersioned(b, PongMarker)
	return Pong{Version: v}, err
}

func appendVersioned(b []byte, marker [4]byte, version string) ([]byte, error) {
	if len(version) > maxVersion {
		return nil, ErrFieldTooLong
	}
	b = append(b, marker[:]...)
	b = append(b, FormatV1, tagPingVersion)
	b = binary.AppendUvarint(b, uint64(len(version)))
	b = append(b, version...)
	return append(b, tagEnd), nil
}

func parseVersioned(b []byte, marker [4]byte) (string, error) {
	switch {
	case len(b) == 0:
		return "", ErrTruncated
	case len(b) < 4 || [4]byte(b[:4]) != marker:
		return "", ErrBadMarker
	case len(b) < 5:
		return "", ErrTruncated
	case b[4] != FormatV1:
		return "", fmt.Errorf("%w: %d", ErrUnknownFormat, b[4])
	}
	b = b[5:]
	var version string
	var haveVersion bool
	for {
		if len(b) == 0 {
			return "", ErrTruncated
		}
		tag := b[0]
		b = b[1:]
		if tag == tagEnd {
			break
		}
		if tag != tagPingVersion {
			return "", fmt.Errorf("%w: tag %d", ErrUnknownField, tag)
		}
		if haveVersion {
			return "", ErrDuplicate
		}
		n, w := binary.Uvarint(b)
		if w <= 0 {
			return "", ErrTruncated
		}
		b = b[w:]
		if n > maxVersion {
			return "", ErrFieldTooLong
		}
		if uint64(len(b)) < n {
			return "", ErrTruncated
		}
		version, haveVersion, b = string(b[:n]), true, b[n:]
	}
	if len(b) != 0 {
		return "", ErrTrailing
	}
	if !haveVersion {
		return "", ErrMissingField
	}
	return version, nil
}
