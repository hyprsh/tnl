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

// Ping and pong field tags. The version is in both; the window flag and the
// Declaration answer are the pong's alone, and the Declaration digest and the
// session id the ping's.
const (
	tagPingVersion = 1
	tagPongInside  = 2 // pong: whether the Outpost is inside the window, 1 byte (0 or 1)
	tagDigest      = 3 // ping: the digest of the Outpost's Declaration, 32 bytes
	tagMatches     = 4 // pong: whether that digest matches the Edge's copy, 1 byte (0 or 1)
	tagRoutes      = 5 // pong: the digest of the Outpost's Routes and waiting lines, 32 bytes
	tagSession     = 6 // ping: the Outpost's session id, 16 bytes
)

const (
	digestSize  = 32
	sessionSize = 16
)

var (
	ErrTruncated = errors.New("tnl: ping is cut short")
	ErrTrailing  = errors.New("tnl: ping has bytes after its end")
	ErrBadFlag   = errors.New("tnl: ping flag is not 0 or 1")
	ErrBadLength = errors.New("tnl: ping field has the wrong length")
)

// Session identifies one run of an Outpost's connection to the Edge, from one
// connect call to the next. The Edge reads a changed id as a restart.
type Session [sessionSize]byte

// Digest is a SHA-256 digest as the ping carries it.
type Digest [digestSize]byte

// Ping is what an Outpost sends the Edge every 10 s.
type Ping struct {
	Version string // the Outpost's version
	// Declaration is the digest of the Outpost's Declaration, an empty one
	// included. Nil leaves the field out, as the Edge's own ping does.
	Declaration *Digest
	// Session is the id of the Outpost's current run. Nil leaves the field out,
	// as the Edge's own ping does.
	Session *Session
}

// Pong is the Edge's answer.
type Pong struct {
	Version string // the Edge's version
	// Inside says whether the Outpost is inside the Edge's version window. A
	// pong without the flag counts as inside.
	Inside bool
	// Declaration is what the Edge says of the Outpost's Declaration. Nil
	// leaves the fields out, as it does for a ping that carried no digest.
	Declaration *DeclarationAnswer
}

// DeclarationAnswer is the Edge's answer about a Declaration.
type DeclarationAnswer struct {
	Matches bool   // the ping's digest matches the Edge's stored copy
	Routes  Digest // of this Outpost's Routes and waiting lines as the Edge has them
}

// AppendPing appends the datagram for p to b.
func AppendPing(b []byte, p Ping) ([]byte, error) {
	f := fields{{tagPingVersion, []byte(p.Version)}}
	if p.Declaration != nil {
		f = append(f, field{tagDigest, p.Declaration[:]})
	}
	if p.Session != nil {
		f = append(f, field{tagSession, p.Session[:]})
	}
	return appendFields(b, PingMarker, f)
}

// AppendPong appends the datagram for p to b.
func AppendPong(b []byte, p Pong) ([]byte, error) {
	f := fields{{tagPingVersion, []byte(p.Version)}, {tagPongInside, []byte{flag(p.Inside)}}}
	if d := p.Declaration; d != nil {
		f = append(f, field{tagMatches, []byte{flag(d.Matches)}}, field{tagRoutes, d.Routes[:]})
	}
	return appendFields(b, PongMarker, f)
}

func flag(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// ParsePing reads a whole datagram, refusing an unknown marker, format number
// or field.
func ParsePing(b []byte) (Ping, error) {
	got, err := parseFields(b, PingMarker, map[byte]int{tagPingVersion: maxVersion, tagDigest: digestSize, tagSession: sessionSize})
	if err != nil {
		return Ping{}, err
	}
	p := Ping{Version: string(got[tagPingVersion])}
	if d, ok := got[tagDigest]; ok {
		if len(d) != digestSize {
			return Ping{}, ErrBadLength
		}
		p.Declaration = (*Digest)(d)
	}
	if v, ok := got[tagSession]; ok {
		if len(v) != sessionSize {
			return Ping{}, ErrBadLength
		}
		p.Session = (*Session)(v)
	}
	return p, nil
}

// PingVersion reads the Outpost's version from a ping that ParsePing may refuse,
// as one from a later release with fields this build does not know. The marker,
// the format number and the version field never change, so any Edge can judge
// any later Outpost by it. Fields before the version are skipped by their
// lengths; nothing after it is read.
func PingVersion(b []byte) (string, bool) {
	if len(b) < 5 || [4]byte(b[:4]) != PingMarker || b[4] != FormatV1 {
		return "", false
	}
	b = b[5:]
	for len(b) > 0 && b[0] != tagEnd {
		tag := b[0]
		n, w := binary.Uvarint(b[1:])
		if w <= 0 || n > uint64(len(b)-1-w) {
			return "", false
		}
		value := b[1+w : 1+w+int(n)]
		if tag == tagPingVersion {
			return string(value), n <= maxVersion
		}
		b = b[1+w+int(n):]
	}
	return "", false
}

// ParsePong reads a whole datagram, refusing what ParsePing refuses. The
// match flag and the Routes digest go together.
func ParsePong(b []byte) (Pong, error) {
	got, err := parseFields(b, PongMarker, map[byte]int{tagPingVersion: maxVersion, tagPongInside: 1, tagMatches: 1, tagRoutes: digestSize})
	if err != nil {
		return Pong{}, err
	}
	p := Pong{Version: string(got[tagPingVersion]), Inside: true}
	if v, ok := got[tagPongInside]; ok {
		if len(v) != 1 || v[0] > 1 {
			return Pong{}, ErrBadFlag
		}
		p.Inside = v[0] == 1
	}
	m, hasM := got[tagMatches]
	r, hasR := got[tagRoutes]
	switch {
	case hasM != hasR:
		return Pong{}, ErrMissingField
	case hasM:
		if len(m) != 1 || m[0] > 1 {
			return Pong{}, ErrBadFlag
		}
		if len(r) != digestSize {
			return Pong{}, ErrBadLength
		}
		p.Declaration = &DeclarationAnswer{Matches: m[0] == 1, Routes: Digest(r)}
	}
	return p, nil
}

type field struct {
	tag   byte
	value []byte
}
type fields []field

func appendFields(b []byte, marker [4]byte, fs fields) ([]byte, error) {
	b = append(b, marker[:]...)
	b = append(b, FormatV1)
	for _, f := range fs {
		if f.tag == tagPingVersion && len(f.value) > maxVersion {
			return nil, ErrFieldTooLong
		}
		b = append(b, f.tag)
		b = binary.AppendUvarint(b, uint64(len(f.value)))
		b = append(b, f.value...)
	}
	return append(b, tagEnd), nil
}

// parseFields reads a whole datagram into its fields. limits holds the tags
// the message may carry and each one's longest value. The version is required.
func parseFields(b []byte, marker [4]byte, limits map[byte]int) (map[byte][]byte, error) {
	switch {
	case len(b) == 0:
		return nil, ErrTruncated
	case len(b) < 4 || [4]byte(b[:4]) != marker:
		return nil, ErrBadMarker
	case len(b) < 5:
		return nil, ErrTruncated
	case b[4] != FormatV1:
		return nil, fmt.Errorf("%w: %d", ErrUnknownFormat, b[4])
	}
	b = b[5:]
	got := map[byte][]byte{}
	for {
		if len(b) == 0 {
			return nil, ErrTruncated
		}
		tag := b[0]
		b = b[1:]
		if tag == tagEnd {
			break
		}
		limit, known := limits[tag]
		if !known {
			return nil, fmt.Errorf("%w: tag %d", ErrUnknownField, tag)
		}
		if _, dup := got[tag]; dup {
			return nil, ErrDuplicate
		}
		n, w := binary.Uvarint(b)
		if w <= 0 {
			return nil, ErrTruncated
		}
		b = b[w:]
		if n > uint64(limit) {
			return nil, ErrFieldTooLong
		}
		if uint64(len(b)) < n {
			return nil, ErrTruncated
		}
		got[tag], b = b[:n:n], b[n:]
	}
	if len(b) != 0 {
		return nil, ErrTrailing
	}
	if _, ok := got[tagPingVersion]; !ok {
		return nil, ErrMissingField
	}
	return got, nil
}
