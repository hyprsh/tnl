package harness

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
)

// A reload of Caddy (a Zone going wildcard and back) drops no connection that
// is waiting to be accepted: a raw TCP Visitor dialling in a loop never sees a
// reset, on connect or on its first read.
func TestReloadResetsNoConnectionWaitingToBeAccepted(t *testing.T) {
	svc := NewHTTPService(t, hello())
	StartFakeDNS(t, "example.test", dnsToken)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "example.test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.example.test", "http://"+svc.Addr(), "nas")

	addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(re.Caddy.HTTPSPort))
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var resets []string
	dials := 0
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
				if err == nil {
					// The server waits for a ClientHello, so only a reset
					// ends this read before the deadline.
					conn.SetReadDeadline(time.Now().Add(3 * time.Millisecond))
					_, err = conn.Read(make([]byte, 1))
					conn.Close()
				}
				mu.Lock()
				dials++
				if errors.Is(err, syscall.ECONNRESET) {
					resets = append(resets, err.Error())
				}
				mu.Unlock()
			}
		}()
	}

	for range 3 {
		if _, err := zoneEdit(t, re, api.ZoneEditRequest{Name: "example.test", Wildcard: "fake", Token: dnsToken}); err != nil {
			t.Fatal(err)
		}
		if _, err := zoneEdit(t, re, api.ZoneEditRequest{Name: "example.test", PerHostname: true}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if dials < 50 {
		t.Errorf("only %d dials", dials)
	}
	if len(resets) != 0 {
		t.Errorf("%d of %d connections were reset, first: %s", len(resets), dials, resets[0])
	}
}
