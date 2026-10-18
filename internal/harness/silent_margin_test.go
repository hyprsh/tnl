package harness

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/tunnel/netstack"
)

// The silence bound at the spec's proportions: pings every half of it, and TCP
// keepalive 5 s idle and 3 s apart against 20 s, so a quarter and 0.15 of it.
const marginBound = 2 * time.Second

// shrinkKeepalive puts the Tunnel's TCP keepalive in the spec's ratio to
// marginBound for the test. Connections made after this take it.
func shrinkKeepalive(t *testing.T) {
	t.Helper()
	idle, interval := netstack.KeepaliveIdle, netstack.KeepaliveInterval
	netstack.KeepaliveIdle, netstack.KeepaliveInterval = marginBound/4, marginBound*3/20
	t.Cleanup(func() { netstack.KeepaliveIdle, netstack.KeepaliveInterval = idle, interval })
}

// A pause on the Visitor's or the Service's side stalls the data on a zero
// window, and gVisor's zero-window probes back off to 120 s, so they do not keep
// the Outpost heard. What does is the Outpost's pings and TCP keepalive on both
// sides. With pings at the spec's ratio to the bound, and with pings out of the
// picture altogether, a paused download and a paused upload are not cut.
func TestPausesLongerThanTheBoundAreNotCutAtTheSpecsProportions(t *testing.T) {
	for _, tc := range []struct {
		name string
		ping time.Duration
	}{
		{"pings at half the bound", marginBound / 2},
		{"no pings after the first", time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shrinkKeepalive(t)
			svc := newSilentService(t, 4*marginBound)
			re, _, logs := silentEdgeWith(t, svc, EdgeOptions{SilentAfter: marginBound, PingInterval: tc.ping})
			client := re.Caddy.Client(t, H1)

			t.Run("download", func(t *testing.T) {
				req, _ := http.NewRequestWithContext(CtxFor(t, 60*time.Second), "GET", "https://app.test/download", nil)
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if _, err := io.ReadFull(resp.Body, make([]byte, 1)); err != nil {
					t.Fatal(err)
				}
				time.Sleep(4 * marginBound)
				n, err := io.Copy(io.Discard, resp.Body)
				if err != nil || n+1 != bigBody {
					t.Fatalf("read %d of %d bytes after the pause: %v", n+1, bigBody, err)
				}
			})
			t.Run("upload", func(t *testing.T) {
				req, _ := http.NewRequestWithContext(CtxFor(t, 60*time.Second), "POST", "https://app.test/upload-stall", zeros(bigBody))
				req.ContentLength = bigBody
				start := time.Now()
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				b, _ := io.ReadAll(resp.Body)
				if resp.StatusCode != 200 || string(b) != "got "+strconv.Itoa(bigBody) {
					t.Fatalf("got %d %q", resp.StatusCode, b)
				}
				if time.Since(start) < svc.pause {
					t.Fatalf("the upload finished in %v: the pause did not stall it", time.Since(start))
				}
			})
			if strings.Contains(logs.String(), "outpost silent") {
				t.Fatalf("the Edge closed the Outpost's connections for silence:\n%s", logs)
			}
		})
	}
}
