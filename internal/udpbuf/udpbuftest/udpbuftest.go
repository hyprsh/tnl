// Package udpbuftest fakes what a UDP socket is granted, for tests of code
// that calls udpbuf.Warn.
package udpbuftest

import (
	"testing"

	"github.com/hyprsh/tnl/internal/udpbuf"
)

// Fake makes udpbuf.Granted report recv and send as the sizes a socket got,
// or err, until the test ends.
func Fake(t *testing.T, recv, send int, err error) {
	t.Helper()
	old := udpbuf.Granted
	t.Cleanup(func() { udpbuf.Granted = old })
	udpbuf.Granted = func() (int, int, error) { return recv, send, err }
}

// Both fakes a socket that got n bytes of each buffer.
func Both(t *testing.T, n int) {
	t.Helper()
	Fake(t, n, n, nil)
}
