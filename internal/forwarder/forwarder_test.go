package forwarder

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/wire"
)

func TestDefaultsMatchTheSpec(t *testing.T) {
	if DefaultDialTimeout != 5*time.Second || DefaultHeaderTimeout != 10*time.Second {
		t.Fatalf("dial %v header %v, want 5s and 10s", DefaultDialTimeout, DefaultHeaderTimeout)
	}
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want wire.Reason
	}{
		{&net.DNSError{Err: "no such host", IsNotFound: true}, wire.ReasonDNS},
		{&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, wire.ReasonRefused},
		{fmt.Errorf("dial: %w", context.DeadlineExceeded), wire.ReasonTimeout},
		{&net.OpError{Op: "dial", Err: syscall.ENETUNREACH}, wire.ReasonRefused},
		{errors.New("other"), wire.ReasonRefused},
	} {
		if got := Classify(tc.err); got != tc.want {
			t.Errorf("Classify(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
