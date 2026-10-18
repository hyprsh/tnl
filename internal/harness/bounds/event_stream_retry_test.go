package bounds

import (
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/edge/liveness"

	. "github.com/hyprsh/tnl/internal/harness"
)

// A browser's EventSource that honours the retry: field makes one request per
// retry interval, so an Offline Outpost costs the Edge a bounded number of log
// lines: one request line per request, and nothing else (ADR 0013).
func TestAnEventStreamClientHonouringRetryCostsOneLogLinePerRequest(t *testing.T) {
	svc := EventService(t)
	log := new(LockedBuf)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), OfflineAfter: ShortOfflineAfter, LogOut: log, LogLevel: slog.LevelInfo})
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	Get(t, re.Caddy, H1, "app.test", "/")
	cutLink(t, o, re)
	o.Stop()
	WaitFor(t, "the Outpost to be Offline", 5*ShortOfflineAfter, func() bool { return re.Liveness.State("nas") == liveness.Offline })
	WaitFor(t, "the Edge to log the outage", 5*time.Second, func() bool { return strings.Contains(log.String(), `msg="outpost offline"`) })
	before := len(strings.Split(strings.TrimSpace(log.String()), "\n"))

	// The client reads the reply to its end and reconnects after the delay the
	// stream gave it. 6 s fits two requests with the 5 s the Edge asks for.
	var requests int
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(Ctx(t), "GET", "https://app.test/sse", nil)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := re.Caddy.Client(t, H1).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		requests++
		field, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "retry: ")
		ms, err := strconv.Atoi(field)
		if resp.StatusCode != 200 || !ok || err != nil {
			t.Fatalf("request %d: got %d %q, want 200 and retry:", requests, resp.StatusCode, b)
		}
		// The client waits out the real retry delay; nothing else ends it.
		time.Sleep(min(time.Duration(ms)*time.Millisecond, time.Until(deadline)))
	}
	if requests != 2 {
		t.Fatalf("%d requests in 6 s, want 2: the client did not wait out the retry", requests)
	}
	lines := strings.Split(strings.TrimSpace(log.String()), "\n")[before:]
	if len(lines) != requests {
		t.Errorf("%d log lines for %d requests, want one each:\n%s", len(lines), requests, strings.Join(lines, "\n"))
	}
}
