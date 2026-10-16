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

// Ping and pong field tags. The version is in both; the window flag is the
// answer's alone. Slice 8 adds the Declaration digests as new tags.
const (
	tagPingVersion = 1
	tagPongInside  = 2
)

var (
	ErrTruncated = errors.New("tnl: ping is cut short")
	ErrTrailing  = errors.New("tnl: ping has bytes after its end")
	ErrBadFlag   = errors.New("tnl: ping flag is not 0 or 1")
)

// Ping is what an Outpost sends the Edge every 10 s.
type Ping struct {
	Version string // the Outpost's version
}

// Pong is the Edge's answer.
type Pong struct {
	Version string // the Edge's version
	// Inside says whether the Outpost is inside the Edge's version window. A
	// pong without the flag counts as inside.
	Inside bool
}

// AppendPing appends the datagram for p to b.
func AppendPing(b []byte, p Ping) ([]byte, error) {
	b, err := appendStart(b, PingMarker, p.Version)
	if err != nil {
		return nil, err
	}
	return append(b, tagEnd), nil
}

// AppendPong appends the datagram for p to b.
func AppendPong(b []byte, p Pong) ([]byte, error) {
	b, err := appendStart(b, PongMarker, p.Version)
	if err != nil {
		return nil, err
	}
	flag := byte(0)
	if p.Inside {
		flag = 1
	}
	b = append(b, tagPongInside, 1, flag)
	return append(b, tagEnd), nil
}

// ParsePing reads a whole datagram, refusing an unknown marker, format number
// or field.
func ParsePing(b []byte) (Ping, error) {
	f, err := parseFields(b, PingMarker, false)
	return Ping{Version: f.version}, err
}

// ParsePong reads a whole datagram, refusing what ParsePing refuses.
func ParsePong(b []byte) (Pong, error) {
	f, err := parseFields(b, PongMarker, true)
	return Pong{Version: f.version, Inside: f.inside}, err
}

func appendStart(b []byte, marker [4]byte, version string) ([]byte, error) {
	if len(version) > maxVersion {
		return nil, ErrFieldTooLong
	}
	b = append(b, marker[:]...)
	b = append(b, FormatV1, tagPingVersion)
	b = binary.AppendUvarint(b, uint64(len(version)))
	return append(b, version...), nil
}

type fields struct {
	version string
	inside  bool
}

// parseFields reads a datagram of the marker's kind. withFlag says whether
// the window flag is a known tag.
func parseFields(b []byte, marker [4]byte, withFlag bool) (fields, error) {
	var out fields
	switch {
	case len(b) == 0:
		return out, ErrTruncated
	case len(b) < 4 || [4]byte(b[:4]) != marker:
		return out, ErrBadMarker
	case len(b) < 5:
		return out, ErrTruncated
	case b[4] != FormatV1:
		return out, fmt.Errorf("%w: %d", ErrUnknownFormat, b[4])
	}
	b = b[5:]
	var haveVersion, haveFlag bool
	out.inside = true
	for {
		if len(b) == 0 {
			return out, ErrTruncated
		}
		tag := b[0]
		b = b[1:]
		if tag == tagEnd {
			break
		}
		if tag != tagPingVersion && !(withFlag && tag == tagPongInside) {
			return out, fmt.Errorf("%w: tag %d", ErrUnknownField, tag)
		}
		if (tag == tagPingVersion && haveVersion) || (tag == tagPongInside && haveFlag) {
			return out, ErrDuplicate
		}
		n, w := binary.Uvarint(b)
		if w <= 0 {
			return out, ErrTruncated
		}
		b = b[w:]
		limit := uint64(maxVersion)
		if tag == tagPongInside {
			limit = 1
		}
		if n > limit {
			return out, ErrFieldTooLong
		}
		if uint64(len(b)) < n {
			return out, ErrTruncated
		}
		val := b[:n]
		b = b[n:]
		if tag == tagPingVersion {
			out.version, haveVersion = string(val), true
			continue
		}
		if n != 1 || val[0] > 1 {
			return out, ErrBadFlag
		}
		out.inside, haveFlag = val[0] == 1, true
	}
	if len(b) != 0 {
		return out, ErrTrailing
	}
	if !haveVersion {
		return out, ErrMissingField
	}
	return out, nil
}
