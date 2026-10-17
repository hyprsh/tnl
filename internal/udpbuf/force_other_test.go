//go:build !linux

package udpbuf

import "testing"

func TestReadBackIsTheGrantOffLinux(t *testing.T) {
	if got := granted(4194304); got != 4194304 {
		t.Errorf("granted(4194304) = %d", got)
	}
}
