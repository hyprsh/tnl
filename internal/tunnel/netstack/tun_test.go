package netstack

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"testing"
	"time"
)

// Close while gVisor is blocked in WriteNotify, handing a packet to a Read that
// never comes, must not panic with a send on a closed channel, and a Read after
// Close reports the device closed.
func TestCloseWhileWriteNotifyIsBlockedSending(t *testing.T) {
	dev, n, err := CreateNetTUN([]netip.Addr{netip.MustParseAddr("100.64.0.2")}, 1280)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dialed := make(chan struct{})
	go func() {
		defer close(dialed)
		// The SYN reaches WriteNotify, which blocks: nobody reads the device.
		n.DialContextTCPAddrPort(ctx, netip.MustParseAddrPort("100.64.0.1:80"))
	}()
	time.Sleep(100 * time.Millisecond)

	if err := dev.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // a late WriteNotify would panic by now
	buf := [][]byte{make([]byte, 1500)}
	if _, err := dev.Read(buf, []int{0}, 0); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Read after Close: %v", err)
	}
	cancel()
	<-dialed
}
