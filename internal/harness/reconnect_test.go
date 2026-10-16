package harness

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/remind"
)

// The Edge saves each Outpost's last WireGuard endpoint, and after a restart
// its first ping is the handshake: every Outpost is back, and its version
// known, within a round trip, not the 25 s of the Outpost's keepalive.
func TestRestartingTheEdgeBringsEveryOutpostBackWithinOneRoundTrip(t *testing.T) {
	// The Outposts ping once at start and then hourly, so only the Edge's own
	// first ping can tell it their versions.
	re := StartEdgeWith(t, EdgeOptions{PingInterval: time.Hour})
	re.JoinOutpost(t, "nas")
	re.JoinOutpost(t, "pi")
	waitFor(t, "both endpoints to be saved", 5*time.Second, func() bool {
		n := 0
		for _, o := range re.Store.Snapshot().Outposts {
			if o.Endpoint.IsValid() {
				n++
			}
		}
		return n == 2
	})
	re.Stop()

	start := time.Now()
	again := StartEdgeWith(t, EdgeOptions{StateDir: re.StateDir, WireGuardPort: re.Port, PingInterval: time.Hour})
	for _, name := range []string{"nas", "pi"} {
		waitFor(t, name+"'s version after the restart", 3*time.Second, func() bool {
			return again.Liveness.Info(name).Version == harnessVersion
		})
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("the Outposts took %v to come back, want about a round trip", d)
	}
	waitFor(t, "the endpoint", time.Second, func() bool { return again.Liveness.Info("nas").Endpoint.IsValid() })
}

// resolver is a fake DNS that the test repoints.
type resolver struct {
	mu sync.Mutex
	ip netip.Addr
}

func (r *resolver) set(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ip = netip.MustParseAddr(ip)
}

func (r *resolver) resolve(context.Context, string) (netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ip, nil
}

const (
	testNoAnswerAfter = 2 * time.Second
	edgeName          = "edge.test"
)

var testReminders = remind.Schedule{After: []time.Duration{time.Second}, Every: time.Second}

func restart(t *testing.T, re *RealEdge) *RealEdge {
	t.Helper()
	re.Stop()
	return StartEdgeWith(t, EdgeOptions{StateDir: re.StateDir, WireGuardPort: re.Port, Address: edgeName})
}

// A routine Edge restart is not worth a warning, while a long outage is
// announced and reminded of on the shared schedule, and the Outpost says when
// the Tunnel is up again and how long it was down.
func TestAnEdgeRestartRaisesNoWarningButALongOutageDoes(t *testing.T) {
	dns := new(resolver)
	dns.set("127.0.0.1")
	re := StartEdgeWith(t, EdgeOptions{Address: edgeName})
	log := new(lockedBuf)
	if _, err := re.TryConnectWith(t, "nas", re.AddOutpost(t, "nas"), mustTempDir(t), ConnectOptions{
		LogOut: log, JoinTimeout: 3 * time.Second, Resolve: dns.resolve, NoAnswerAfter: testNoAnswerAfter, Reminders: testReminders,
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first Tunnel up", 5*time.Second, func() bool { return strings.Contains(log.String(), "Tunnel up") })

	// Routine: back well inside the silence the warning needs.
	re = restart(t, re)
	waitFor(t, "the Outpost to be heard again", 3*time.Second, func() bool { return re.Liveness.Info("nas").Version == harnessVersion })
	time.Sleep(testNoAnswerAfter + 500*time.Millisecond)
	if hasWarn(log.String()) {
		t.Fatalf("a routine restart logged a warning:\n%s", log)
	}

	// Long: the Edge stays down.
	re.Stop()
	waitFor(t, "the warning", 2*testNoAnswerAfter+2*time.Second, func() bool { return strings.Contains(log.String(), "No answer from the Edge") })
	line := lineWith(log.String(), "No answer from the Edge")
	for _, want := range []string{"level=warn", "edge_address=" + edgeName, "edge_ip=127.0.0.1", "the Edge is down or unreachable, or this Outpost was removed or rekeyed on the Edge"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the warning lacks %q: %s", want, line)
		}
	}
	waitFor(t, "a reminder", 3*time.Second, func() bool { return strings.Contains(log.String(), "Still no answer from the Edge") })
	if n := strings.Count(log.String(), "msg=\"No answer from the Edge\""); n != 1 {
		t.Fatalf("%d first warnings, want 1", n)
	}

	re = restart(t, re)
	waitFor(t, "Tunnel up again", 5*time.Second, func() bool { return strings.Count(log.String(), "Tunnel up") == 2 })
	up := lineWith(log.String()[strings.LastIndex(log.String(), "Still no answer"):], "Tunnel up")
	for _, want := range []string{"level=info", "edge_address=" + edgeName, "edge_ip=127.0.0.1", "edge_version=" + harnessVersion, "outage="} {
		if !strings.Contains(up, want) {
			t.Fatalf("Tunnel up lacks %q: %s", want, up)
		}
	}
}

// An Outpost whose --address starts resolving to a new IP follows it and logs
// the change, once the Tunnel has been down for the silence.
func TestAnOutpostFollowsTheEdgeToANewAddress(t *testing.T) {
	dns := new(resolver)
	dns.set("127.0.0.1")
	re := StartEdgeWith(t, EdgeOptions{Address: edgeName})
	log := new(lockedBuf)
	o, err := re.TryConnectWith(t, "nas", re.AddOutpost(t, "nas"), mustTempDir(t), ConnectOptions{
		LogOut: log, JoinTimeout: 3 * time.Second, Resolve: dns.resolve, NoAnswerAfter: time.Second, Reminders: testReminders,
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := func() string {
		out, _ := o.Tunnel.Dev.IpcGet()
		return lineWith(out, "endpoint=")
	}

	re.Stop()
	dns.set("192.0.2.1") // TEST-NET: nothing answers, which is not the point here
	waitFor(t, "the Outpost to switch", 5*time.Second, func() bool { return strings.Contains(log.String(), "the Edge's address changed") })
	if line := lineWith(log.String(), "the Edge's address changed"); !strings.Contains(line, "level=info") || !strings.Contains(line, "old_ip=127.0.0.1") || !strings.Contains(line, "edge_ip=192.0.2.1") {
		t.Fatalf("change line: %s", line)
	}
	if got := endpoint(); !strings.HasPrefix(got, "endpoint=192.0.2.1:") {
		t.Fatalf("the Tunnel's endpoint is %q, want it to be 192.0.2.1", got)
	}

	// The Edge is back at its old address and the Outpost finds it there.
	dns.set("127.0.0.1")
	again := StartEdgeWith(t, EdgeOptions{StateDir: re.StateDir, WireGuardPort: re.Port, Address: edgeName})
	waitFor(t, "the Outpost to come back", 10*time.Second, func() bool { return again.Liveness.Info("nas").Version == harnessVersion })
	if got := endpoint(); !strings.HasPrefix(got, "endpoint=127.0.0.1:") {
		t.Fatalf("the Tunnel's endpoint is %q, want 127.0.0.1", got)
	}
}

func hasWarn(log string) bool { return strings.Contains(log, "level=warn") }
