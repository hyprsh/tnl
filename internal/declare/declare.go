// Package declare holds what the Edge and an Outpost must agree on about a
// Declaration: its lines as they travel, and the two digests the ping carries.
// It imports nothing, so both builds link it.
package declare

import (
	"crypto/sha256"
	"sort"
)

// Line is one declared Route as it travels and as the digests see it. The
// Service is canonical and the Hostname lower-case. The line's label never
// reaches the Edge.
type Line struct {
	Hostname           string `json:"hostname"`
	Service            string `json:"service"`
	HostHeader         string `json:"host_header,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
}

// Entry is a line as the Edge has it: applied (it is a Route) or waiting.
type Entry struct {
	Line
	Waiting bool `json:"waiting,omitempty"`
}

// DigestSize is the length of a digest in bytes.
const DigestSize = sha256.Size

// Digest is the digest of a Declaration: SHA-256 over a canonical text, so it
// does not depend on the order of the lines, on JSON key order or on
// whitespace. The text is the prefix "tnl-declaration-1\n", then for each line
// in Hostname order its Hostname, Service, host header and "1" or "0" for
// insecure_skip_verify, each followed by a NUL byte, and a newline. An empty
// Declaration has a digest too.
func Digest(lines []Line) [DigestSize]byte {
	entries := make([]Entry, len(lines))
	for i, l := range lines {
		entries[i].Line = l
	}
	return digest("tnl-declaration-1\n", entries, false)
}

// RoutesDigest is the digest of an Outpost's Routes and waiting lines as the
// Edge has them: the same text under the prefix "tnl-routes-1\n", with a
// further "waiting" or "applied" field in each line's record.
func RoutesDigest(entries []Entry) [DigestSize]byte {
	return digest("tnl-routes-1\n", entries, true)
}

func digest(prefix string, entries []Entry, withStatus bool) [DigestSize]byte {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Hostname < sorted[j].Hostname })
	h := sha256.New()
	field := func(s string) { h.Write([]byte(s)); h.Write([]byte{0}) }
	h.Write([]byte(prefix))
	for _, e := range sorted {
		field(e.Hostname)
		field(e.Service)
		field(e.HostHeader)
		if e.InsecureSkipVerify {
			field("1")
		} else {
			field("0")
		}
		if withStatus {
			if e.Waiting {
				field("waiting")
			} else {
				field("applied")
			}
		}
		h.Write([]byte{'\n'})
	}
	var out [DigestSize]byte
	h.Sum(out[:0])
	return out
}
