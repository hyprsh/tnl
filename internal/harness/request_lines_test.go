package harness

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

// requestLines returns the log lines the Edge wrote about requests.
func requestLines(lines *logCapture) []string {
	var out []string
	for _, l := range strings.Split(lines.String(), "\n") {
		if strings.Contains(l, "msg=request") {
			out = append(out, l)
		}
	}
	return out
}

func visit(t *testing.T, ce *CaddyEdge, proto Proto, host, pathAndQuery string, hdr map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx(t), "GET", "https://"+host+pathAndQuery, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := ce.Client(t, proto).Do(req)
	if err != nil {
		t.Fatalf("%s: %v", proto, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestA404InsideAZoneIsInfoAndOutsideEveryZoneIsDebug(t *testing.T) {
	log, lines := newLogCapture()
	h := New(t)
	ce := h.Edge.StartCaddy(t, CaddyOptions{Log: log, InZone: func(host string) bool { return host == "inside.test" }})
	visit(t, ce, H1, "inside.test", "/", nil)
	visit(t, ce, H1, "outside.test", "/", nil)
	got := requestLines(lines)
	if len(got) != 2 {
		t.Fatalf("%d request lines, want 2:\n%s", len(got), strings.Join(got, "\n"))
	}
	for _, l := range got {
		switch {
		case strings.Contains(l, "host=inside.test"):
			if !strings.Contains(l, "level=INFO") || !strings.Contains(l, "status=404") || !strings.Contains(l, `reason="no route"`) {
				t.Errorf("a 404 inside a Zone: %s", l)
			}
		case strings.Contains(l, "host=outside.test"):
			if !strings.Contains(l, "level=DEBUG") || !strings.Contains(l, "status=404") {
				t.Errorf("a 404 outside every Zone: %s", l)
			}
		}
	}
}

func TestEverySelfMadeResponseGetsOneLineWithTheSpecsFields(t *testing.T) {
	log, lines := newLogCapture()
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home"})
	ce := h.Edge.StartCaddy(t, CaddyOptions{Log: log})
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "ghost.test", Outpost: "gone", Service: closedAddr(t)}, {Hostname: "down.test", Outpost: o.Name, Service: closedAddr(t)}})
	visit(t, ce, H2, "ghost.test", "/page?secret=1", map[string]string{"Cookie": "sid=abc", "User-Agent": "agent-007", "Referer": "https://ref.example/"})
	got := requestLines(lines)
	if len(got) != 1 {
		t.Fatalf("%d request lines, want 1:\n%s", len(got), strings.Join(got, "\n"))
	}
	l := got[0]
	for _, want := range []string{"level=INFO", "id=", "visitor=127.0.0.1:", "host=ghost.test", "method=GET", "path=/page", "proto=HTTP/2.0",
		"status=502", "duration=", "bytes_in=0", "bytes_out=", `reason="outpost offline"`, "outpost=gone"} {
		if !strings.Contains(l, want) {
			t.Errorf("line lacks %q: %s", want, l)
		}
	}
	for _, never := range []string{"secret", "sid=", "abc", "agent-007", "ref.example", "?"} {
		if strings.Contains(strings.ToLower(strings.Join(got, "\n")), never) {
			t.Errorf("the log contains %q:\n%s", never, lines.String())
		}
	}
}

func TestSuccessfulRequestsLogNothingWithoutAccessLog(t *testing.T) { successfulRequests(t, false) }

func TestSuccessfulRequestsAreLoggedWithAccessLog(t *testing.T) { successfulRequests(t, true) }

func successfulRequests(t *testing.T, access bool) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, "hello")
	}))
	{
		log, lines := newLogCapture()
		h := New(t)
		o := h.AddOutpost(t, OutpostOptions{Name: "home"})
		ce := h.Edge.StartCaddy(t, CaddyOptions{Log: log, AccessLog: access})
		h.Edge.Routes.Swap([]routes.Route{{Hostname: "ok.test", Outpost: o.Name, Service: svc.Addr()}})
		visit(t, ce, H1, "ok.test", "/hello?token=hunter2", map[string]string{"User-Agent": "agent-007"})
		visit(t, ce, H1, "ok.test", "/missing", nil)
		got := requestLines(lines)
		if !access {
			if len(got) != 0 {
				t.Fatalf("without --access-log: %s", strings.Join(got, "\n"))
			}
			return
		}
		if len(got) != 2 {
			t.Fatalf("with --access-log: %d lines, want 2:\n%s", len(got), strings.Join(got, "\n"))
		}
		if l := got[0]; !strings.Contains(l, "level=INFO") || !strings.Contains(l, "status=200") || !strings.Contains(l, "path=/hello") ||
			!strings.Contains(l, "bytes_out=5") || !strings.Contains(l, "outpost=home") || strings.Contains(l, "reason") {
			t.Errorf("the successful request: %s", l)
		}
		if !strings.Contains(got[1], "status=404") {
			t.Errorf("what the Service answered: %s", got[1])
		}
		for _, never := range []string{"hunter2", "token", "agent-007", "?"} {
			if strings.Contains(strings.Join(got, "\n"), never) {
				t.Errorf("the log contains %q:\n%s", never, lines.String())
			}
		}
	}
}
