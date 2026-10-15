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
	a := Edge.As4()
	v := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	v += uint32(n) + 1
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}
