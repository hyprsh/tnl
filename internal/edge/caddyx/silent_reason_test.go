package caddyx

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/edge/conntrack"
	"github.com/hyprsh/tnl/internal/edge/liveness"
)

// lineFor is the request line of a request that was running on c when it
// failed for a reason of its own, as the Router would write it. Empty if none.
func lineFor(t *testing.T, c net.Conn) string {
	t.Helper()
	var out bytes.Buffer
	rt := &Runtime{
		Liveness: fixedState(liveness.Online),
		Log:      slog.New(slog.NewTextHandler(&out, nil)),
	}
	a := rt.begin(func() {}, false)
	if c != nil {
		a.setConn(c)
	}
	req := httptest.NewRequest("GET", "http://app.test/", nil)
	rt.writeRequestLine(requestLog{
		req: req, id: "r1", rec: &recorder{ResponseWriter: httptest.NewRecorder()},
		outpost: "home", started: time.Now().Add(-time.Minute), a: a,
		failed: context.Canceled, inZone: true,
		vars: func(string) any { return nil },
	})
	return strings.TrimSpace(out.String())
}

func trackedPipe(t *testing.T, tr *conntrack.Tracker) net.Conn {
	t.Helper()
	c, s := net.Pipe()
	t.Cleanup(func() { c.Close(); s.Close() })
	return tr.Track("app.test", "home", c)
}

func TestARequestOnAConnectionTheSilenceClosedIsLoggedAsOutpostSilent(t *testing.T) {
	tr := conntrack.New()
	c := trackedPipe(t, tr)
	tr.Silence("home")
	if line := lineFor(t, c); !strings.Contains(line, `reason="outpost silent"`) {
		t.Fatalf("line %q", line)
	}
}

// A request that waited through a silence, was served on a connection opened
// after the close, and failed later for its own reason was not cut by the
// silence.
func TestARequestOnAConnectionOpenedAfterTheSilenceIsNotLoggedAsOutpostSilent(t *testing.T) {
	tr := conntrack.New()
	trackedPipe(t, tr)
	tr.Silence("home")
	later := trackedPipe(t, tr)
	if line := lineFor(t, later); line != "" {
		t.Fatalf("line %q, want none", line)
	}
}

func TestARequestWithNoConnectionIsNotLoggedAsOutpostSilent(t *testing.T) {
	tr := conntrack.New()
	trackedPipe(t, tr)
	tr.Silence("home")
	if line := lineFor(t, nil); line != "" {
		t.Fatalf("line %q, want none", line)
	}
}

// The Transport hands the request the connection it picked; over TLS that is a
// tls.Conn around the tracked one.
func TestTheSilenceMarkIsFoundThroughATLSConnection(t *testing.T) {
	tr := conntrack.New()
	c := trackedPipe(t, tr)
	tr.Silence("home")
	if line := lineFor(t, wrapped{c}); !strings.Contains(line, `reason="outpost silent"`) {
		t.Fatalf("line %q", line)
	}
}

type wrapped struct{ net.Conn }

func (w wrapped) NetConn() net.Conn { return w.Conn }
