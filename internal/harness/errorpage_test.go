package harness

import (
	"context"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

func assertAnonymous(t *testing.T, resp *http.Response, body string) {
	t.Helper()
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", resp.Header.Get("Cache-Control"))
	}
	var hdrs strings.Builder
	for k, vs := range resp.Header {
		hdrs.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
	}
	all := strings.ToLower(body + hdrs.String())
	for _, name := range []string{"tnl", "home", "outpost", "127.0.0.1"} {
		if strings.Contains(all, name) {
			t.Errorf("answer mentions %q:\n%s", name, all)
		}
	}
}

func TestNoRouteGetsThe404Page(t *testing.T) {
	log, lines := NewLogCapture()
	h := New(t)
	ce := h.Edge.StartCaddy(t, CaddyOptions{Log: log})
	for _, proto := range Protos {
		resp, body := GetAccepting(t, ce, proto, "other.test", "text/html")
		if resp.StatusCode != 404 || !strings.Contains(body, "Nothing is served at other.test") || !strings.Contains(body, "<style>") {
			t.Fatalf("%s: got %d %q", proto, resp.StatusCode, body)
		}
		assertAnonymous(t, resp, body)

		resp, body = GetAccepting(t, ce, proto, "other.test", "")
		if resp.StatusCode != 404 || body != "404 Nothing is served at other.test\n" {
			t.Fatalf("%s: plain text: got %d %q", proto, resp.StatusCode, body)
		}
		assertAnonymous(t, resp, body)
	}
	if got := lines.String(); !strings.Contains(got, "level=info") || !strings.Contains(got, "status=404") || !strings.Contains(got, `reason="no route"`) {
		t.Errorf("the 404 is not logged at info with its reason:\n%s", got)
	}
}

func TestUnreachableServiceGetsThe502PageWithTheIDOfTheLog(t *testing.T) {
	log, lines := NewLogCapture()
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home"})
	ce := h.Edge.StartCaddy(t, CaddyOptions{Log: log})
	closed := ClosedAddr(t)
	h.Edge.Routes.Swap([]routes.Route{
		{Hostname: "refused.test", Outpost: o.Name, Service: closed},
		{Hostname: "ghost.test", Outpost: "no-such-outpost", Service: closed},
	})
	idRe := regexp.MustCompile(`[0-9a-f]{16}`)
	for _, proto := range Protos {
		resp, body := GetAccepting(t, ce, proto, "ghost.test", "text/html")
		if resp.StatusCode != 503 || !strings.Contains(body, "ghost.test is offline") {
			t.Fatalf("%s: got %d %q", proto, resp.StatusCode, body)
		}
		assertAnonymous(t, resp, body)
		id := idRe.FindString(body)
		if id == "" || !strings.Contains(lines.String(), "id="+id) {
			t.Errorf("%s: the page's request ID %q is not in the log:\n%s", proto, id, lines.String())
		}
		resp, body = GetAccepting(t, ce, proto, "ghost.test", "application/json")
		if resp.StatusCode != 503 || body != "503 ghost.test is offline\n" {
			t.Fatalf("%s: plain text: got %d %q", proto, resp.StatusCode, body)
		}
	}
}

func TestRefusedServiceLogsItsReasonAfterTheRetries(t *testing.T) {
	log, lines := NewLogCapture()
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home"})
	ce := h.Edge.StartCaddy(t, CaddyOptions{Log: log})
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "refused.test", Outpost: o.Name, Service: ClosedAddr(t)}})

	start := time.Now()
	resp, body := GetAccepting(t, ce, H1, "refused.test", "text/html")
	took := time.Since(start)
	if resp.StatusCode != 502 || !strings.Contains(body, "refused.test") {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	if took < 4500*time.Millisecond || took > 8*time.Second {
		t.Errorf("502 after %v, want about 5 s", took)
	}
	got := lines.String()
	if !strings.Contains(got, "status=502") || !strings.Contains(got, "reason=refused") || !strings.Contains(got, "level=info") {
		t.Errorf("the 502 is not logged at info with its reason:\n%s", got)
	}
	// The Visitor's page never says why.
	if strings.Contains(strings.ToLower(body), "refused") && !strings.Contains(body, "refused.test") {
		t.Errorf("page gives the reason: %s", body)
	}
}

func TestServiceThatStartsListeningLaterIsServed(t *testing.T) {
	addr := ClosedAddr(t)
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home"})
	ce := h.Edge.StartCaddy(t, CaddyOptions{})
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "late.test", Outpost: o.Name, Service: addr}})

	go func() {
		time.Sleep(2 * time.Second)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "finally") })}
		t.Cleanup(func() { srv.Close() })
		srv.Serve(ln)
	}()
	start := time.Now()
	resp, body := GetAccepting(t, ce, H1, "late.test", "text/html")
	if resp.StatusCode != 200 || body != "finally" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	if took := time.Since(start); took < 1900*time.Millisecond || took > 4*time.Second {
		t.Errorf("served after %v, want about 2 s", took)
	}
}

func TestTimeoutIsNotRetried(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home", Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		attempts++
		mu.Unlock()
		return nil, context.DeadlineExceeded
	}})
	ce := h.Edge.StartCaddy(t, CaddyOptions{})
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "slow.test", Outpost: o.Name, Service: "10.0.0.1:80"}})

	start := time.Now()
	resp, _ := GetAccepting(t, ce, H1, "slow.test", "text/html")
	if resp.StatusCode != 502 {
		t.Fatalf("got %d, want 502", resp.StatusCode)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("a timeout took %v, want no retries", took)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Errorf("%d dial attempts, want 1", attempts)
	}
}

func TestEventStreamToADownServiceGetsRetryAndIsClosed(t *testing.T) {
	log, lines := NewLogCapture()
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home"})
	ce := h.Edge.StartCaddy(t, CaddyOptions{Log: log})
	h.Edge.Routes.Swap([]routes.Route{
		{Hostname: "down.test", Outpost: o.Name, Service: ClosedAddr(t)},
		{Hostname: "ghost.test", Outpost: "no-such-outpost", Service: ClosedAddr(t)},
	})
	for host, status := range map[string]string{"ghost.test": "status=503", "down.test": "status=502"} {
		for _, proto := range Protos {
			req, _ := http.NewRequestWithContext(Ctx(t), "GET", "https://"+host+"/events", nil)
			req.Header.Set("Accept", "text/event-stream")
			resp, err := ce.Client(t, proto).Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v, want a 200 answer", proto, host, err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatalf("%s %s: the stream did not close cleanly: %v", proto, host, err)
			}
			if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") || string(body) != "retry: 5000\n\n" {
				t.Fatalf("%s %s: got %d %q %q, want 200 text/event-stream with retry: 5000", proto, host, resp.StatusCode, resp.Header.Get("Content-Type"), body)
			}
		}
		// One line per request with the real status; retry: 5000 bounds a
		// browser to one line per 5 s.
		got := lines.String()
		if !strings.Contains(got, "host="+host) || !strings.Contains(got, status) || !strings.Contains(got, "event_stream=true") || strings.Contains(got, "reset=") {
			t.Errorf("%s: the line does not log the real status:\n%s", host, got)
		}
	}
}
