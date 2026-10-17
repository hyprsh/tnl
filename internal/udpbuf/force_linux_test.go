package udpbuf

import "testing"

// rmem_max=4194304 reads back 8388608, which must not pass the 7 MiB check.
func TestLinuxReadBackIsHalvedToTheGrant(t *testing.T) {
	if got := granted(8388608); got != 4194304 {
		t.Errorf("granted(8388608) = %d, want 4194304", got)
	}
	if got := granted(7500000 * 2); got < Request {
		t.Errorf("a grant of the documented sysctl value reads as %d, under the request", got)
	}
}
