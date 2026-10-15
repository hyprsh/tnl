// Package wire encodes the tnl header and reply that start every Tunnel
// connection from the Edge to an Outpost's data port. It has no dependencies,
// so both roles share it. The encoding is recorded in docs/spec/v1.md, section 4.
package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// DataPort is the port the Outpost's forwarder listens on, on its Tunnel
// address. It exists only inside the netstacks, so it can't clash with a host port.
const DataPort = 7000

// Marker starts every tnl header. The format number follows it.
var Marker = [4]byte{'t', 'n', 'l', 0}

// FormatV1 is the only header format so far.
const FormatV1 = 1

// Header field tags. A header is Marker, format, then fields as
// tag byte, uvarint length, bytes, ended by tagEnd.
const (
	tagEnd       = 0
	tagService   = 1
	tagHostname  = 2
	tagRequestID = 3
)

// Field length caps, so a bad peer can't make the Outpost allocate.
const (
	maxService   = 255
	maxHostname  = 255
	maxRequestID = 64
	maxVersion   = 64
)

var (
	ErrBadMarker     = errors.New("tnl: not a tnl header (bad marker)")
	ErrUnknownFormat = errors.New("tnl: unknown header format number")
	ErrUnknownField  = errors.New("tnl: unknown header field")
	ErrFieldTooLong  = errors.New("tnl: header field too long")
	ErrMissingField  = errors.New("tnl: header lacks the Service")
	ErrDuplicate     = errors.New("tnl: header field repeated")
)

// Header names what the Edge wants the Outpost to dial.
type Header struct {
	Service   string // the Service's host:port
	Hostname  string // the Route's Hostname, for the Outpost's logs
	RequestID string
}

// WriteHeader writes h in the current format.
func WriteHeader(w io.Writer, h Header) error {
	if h.Service == "" {
		return ErrMissingField
	}
	var b bytes.Buffer
	b.Write(Marker[:])
	b.WriteByte(FormatV1)
	for _, f := range []struct {
		tag byte
		val string
		max int
	}{
		{tagService, h.Service, maxService},
		{tagHostname, h.Hostname, maxHostname},
		{tagRequestID, h.RequestID, maxRequestID},
	} {
		if len(f.val) > f.max {
			return ErrFieldTooLong
		}
		b.WriteByte(f.tag)
		b.Write(binary.AppendUvarint(nil, uint64(len(f.val))))
		b.WriteString(f.val)
	}
	b.WriteByte(tagEnd)
	_, err := w.Write(b.Bytes())
	return err
}

// ReadHeader reads one header, refusing an unknown marker, format number or field.
// It reads no more than the header, so the caller can go on reading request bytes
// from the same stream if r is not buffered.
func ReadHeader(r io.Reader) (Header, error) {
	br := byteReader{r: r}
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return Header{}, err
	}
	if [4]byte(head[:4]) != Marker {
		return Header{}, ErrBadMarker
	}
	if head[4] != FormatV1 {
		return Header{}, fmt.Errorf("%w: %d", ErrUnknownFormat, head[4])
	}
	var h Header
	seen := map[byte]bool{}
	for {
		tag, err := br.ReadByte()
		if err != nil {
			return Header{}, err
		}
		if tag == tagEnd {
			break
		}
		var dst *string
		var max int
		switch tag {
		case tagService:
			dst, max = &h.Service, maxService
		case tagHostname:
			dst, max = &h.Hostname, maxHostname
		case tagRequestID:
			dst, max = &h.RequestID, maxRequestID
		default:
			return Header{}, fmt.Errorf("%w: tag %d", ErrUnknownField, tag)
		}
		if seen[tag] {
			return Header{}, ErrDuplicate
		}
		seen[tag] = true
		n, err := binary.ReadUvarint(&br)
		if err != nil {
			return Header{}, err
		}
		if n > uint64(max) {
			return Header{}, ErrFieldTooLong
		}
		val := make([]byte, n)
		if _, err := io.ReadFull(r, val); err != nil {
			return Header{}, err
		}
		*dst = string(val)
	}
	if h.Service == "" {
		return Header{}, ErrMissingField
	}
	return h, nil
}

// byteReader reads single bytes without buffering ahead.
type byteReader struct {
	r io.Reader
}

func (b *byteReader) ReadByte() (byte, error) {
	var one [1]byte
	_, err := io.ReadFull(b.r, one[:])
	return one[0], err
}

var _ io.ByteReader = (*byteReader)(nil)

// Status is the first byte of a reply.
type Status byte

const (
	StatusOK          Status = 0
	StatusDialError   Status = 1
	StatusUnsupported Status = 2
)

// Reason says why a dial failed.
type Reason byte

const (
	ReasonNone      Reason = 0
	ReasonRefused   Reason = 1
	ReasonTimeout   Reason = 2
	ReasonDNS       Reason = 3
	ReasonAllowlist Reason = 4
)

func (r Reason) String() string {
	switch r {
	case ReasonRefused:
		return "refused"
	case ReasonTimeout:
		return "timeout"
	case ReasonDNS:
		return "dns"
	case ReasonAllowlist:
		return "allowlist"
	}
	return fmt.Sprintf("reason(%d)", byte(r))
}

// Reply is the Outpost's answer, sent before any request byte.
//
// Its bytes are: status; for a dial error one reason byte; for unsupported a
// length byte and the Outpost's version. It has no marker or format number and
// never changes across majors, so a mismatch is always reported, never misparsed.
type Reply struct {
	Status  Status
	Reason  Reason // when Status is StatusDialError
	Version string // when Status is StatusUnsupported
}

// WriteReply writes rp.
func WriteReply(w io.Writer, rp Reply) error {
	b := []byte{byte(rp.Status)}
	switch rp.Status {
	case StatusOK:
	case StatusDialError:
		b = append(b, byte(rp.Reason))
	case StatusUnsupported:
		if len(rp.Version) > maxVersion {
			return ErrFieldTooLong
		}
		b = append(b, byte(len(rp.Version)))
		b = append(b, rp.Version...)
	default:
		return fmt.Errorf("tnl: unknown reply status %d", rp.Status)
	}
	_, err := w.Write(b)
	return err
}

// ReadReply reads one reply, reading no further.
func ReadReply(r io.Reader) (Reply, error) {
	br := byteReader{r: r}
	st, err := br.ReadByte()
	if err != nil {
		return Reply{}, err
	}
	rp := Reply{Status: Status(st)}
	switch rp.Status {
	case StatusOK:
	case StatusDialError:
		b, err := br.ReadByte()
		if err != nil {
			return Reply{}, err
		}
		rp.Reason = Reason(b)
		if rp.Reason < ReasonRefused || rp.Reason > ReasonAllowlist {
			return Reply{}, fmt.Errorf("tnl: unknown dial error reason %d", b)
		}
	case StatusUnsupported:
		n, err := br.ReadByte()
		if err != nil {
			return Reply{}, err
		}
		if n > maxVersion {
			return Reply{}, ErrFieldTooLong
		}
		v := make([]byte, n)
		if _, err := io.ReadFull(r, v); err != nil {
			return Reply{}, err
		}
		rp.Version = string(v)
	default:
		return Reply{}, fmt.Errorf("tnl: unknown reply status %d", st)
	}
	return rp, nil
}
