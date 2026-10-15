// Package addr holds the Tunnel address plan: everything lives in 100.64.0.0/10
// and exists only inside the netstacks.
package addr

import "net/netip"

// Range is the Tunnel range. It isn't configurable.
var Range = netip.MustParsePrefix("100.64.0.0/10")

// Edge is the Edge's address in the Tunnel.
var Edge = netip.MustParseAddr("100.64.0.1")

// Outpost returns the address of the nth Outpost added, counting from 0:
// the first is 100.64.0.2.
func Outpost(n int) netip.Addr {
	v := ip32(Edge) + uint32(n) + 1
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// OutpostIndex is the inverse of Outpost: it returns n such that a ==
// Outpost(n), or false if a is not an Outpost address in Range.
func OutpostIndex(a netip.Addr) (int, bool) {
	if !a.Is4() || !Range.Contains(a) {
		return 0, false
	}
	n := int64(ip32(a)) - int64(ip32(Edge)) - 1
	if n < 0 {
		return 0, false
	}
	return int(n), true
}

func ip32(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
