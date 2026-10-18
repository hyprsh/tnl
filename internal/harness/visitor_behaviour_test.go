package harness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

func TestRouteSwapServesTheNextRequestWithoutAReload(t *testing.T) {
	a := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "a") }))
	b := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "b") }))
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home"})
	ce := h.Edge.StartCaddy(t, CaddyOptions{})

	if resp, _ := Get(t, ce, H1, "app.test", "/"); resp.StatusCode != 404 {
		t.Fatalf("before any Route: %d, want 404", resp.StatusCode)
	}
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "app.test", Outpost: o.Name, Service: a.Addr()}})
	if _, body := Get(t, ce, H1, "app.test", "/"); body != "a" {
		t.Fatalf("got %q, want a", body)
	}
	// The same Hostname now points elsewhere: the pooled connection to a must not be used.
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "app.test", Outpost: o.Name, Service: b.Addr()}})
	if _, body := Get(t, ce, H1, "app.test", "/"); body != "b" {
		t.Fatalf("got %q, want b", body)
	}
	h.Edge.Routes.Swap(nil)
	if resp, _ := Get(t, ce, H1, "app.test", "/"); resp.StatusCode != 404 {
		t.Fatalf("after the Route is gone: %d, want 404", resp.StatusCode)
	}
}

func TestPort80RedirectsWithA308(t *testing.T) {
	h := EdgeWithService(t, http.NotFoundHandler())
	ce := h.Edge.Caddy
	for _, host := range []string{"app.test", "unrouted.test"} {
		req, _ := http.NewRequestWithContext(Ctx(t), "GET", "http://"+host+"/foo?a=1", nil)
		resp, err := ce.PlainClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		want := fmt.Sprintf("https://%s:%d/foo?a=1", host, ce.HTTPSPort)
		if resp.StatusCode != 308 || resp.Header.Get("Location") != want {
			t.Fatalf("%s: got %d Location %q, want 308 %q", host, resp.StatusCode, resp.Header.Get("Location"), want)
		}
	}
}

func TestServiceSeesTheVisitorsHostAndForwardedHeadersButNoRequestID(t *testing.T) {
	h := EdgeWithService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := r.Header.Clone()
		hdr.Set("Seen-Host", r.Host)
		json.NewEncoder(w).Encode(hdr)
	}))
	hexID := regexp.MustCompile(`^[0-9a-f]{16}$`)
	for _, proto := range Protos {
		t.Run(string(proto), func(t *testing.T) {
			_, body := Get(t, h.Edge.Caddy, proto, "app.test", "/")
			var hdr http.Header
			if err := json.Unmarshal([]byte(body), &hdr); err != nil {
				t.Fatal(err)
			}
			if hdr.Get("Seen-Host") != "app.test" {
				t.Errorf("Host = %q, want the Visitor's app.test", hdr.Get("Seen-Host"))
			}
			if hdr.Get("X-Forwarded-Proto") != "https" || hdr.Get("X-Forwarded-Host") != "app.test" || hdr.Get("X-Forwarded-For") != "127.0.0.1" {
				t.Errorf("X-Forwarded-* = %v", hdr)
			}
			for k, vs := range hdr {
				if strings.Contains(strings.ToLower(k), "request") || strings.Contains(strings.ToLower(k), "id") {
					t.Errorf("header %s looks like a request ID", k)
				}
				for _, v := range vs {
					if hexID.MatchString(v) {
						t.Errorf("header %s = %q looks like a request ID", k, v)
					}
				}
			}
		})
	}
}

func TestEventStreamIsFlushedOnEveryWrite(t *testing.T) {
	for _, proto := range Protos {
		t.Run(string(proto), func(t *testing.T) {
			next := make(chan struct{})
			h := EdgeWithService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: one\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-next:
					fmt.Fprint(w, "data: two\n\n")
				case <-r.Context().Done():
				}
			}))
			req, _ := http.NewRequestWithContext(Ctx(t), "GET", "https://app.test/", nil)
			resp, err := h.Edge.Caddy.Client(t, proto).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			br := bufio.NewReader(resp.Body)
			// The first event must arrive while the Service is still holding the stream open.
			line, err := br.ReadString('\n')
			if err != nil || line != "data: one\n" {
				t.Fatalf("first event: %q, %v", line, err)
			}
			close(next)
			br.ReadString('\n')
			line, err = br.ReadString('\n')
			if err != nil || line != "data: two\n" {
				t.Fatalf("second event: %q, %v", line, err)
			}
		})
	}
}

func TestBodiesStreamUnbufferedBothWays(t *testing.T) {
	// The Service answers after the first chunk of the upload and keeps reading.
	// A buffering Edge would hold both until the upload ends.
	h := EdgeWithService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NewResponseController(w).EnableFullDuplex()
		buf := make([]byte, 5)
		io.ReadFull(r.Body, buf)
		fmt.Fprintf(w, "got %s", buf)
		w.(http.Flusher).Flush()
		io.Copy(io.Discard, r.Body)
	}))
	pr, pw := io.Pipe()
	defer pw.Close()
	req, _ := http.NewRequestWithContext(Ctx(t), "POST", "https://app.test/", pr)
	go pw.Write([]byte("chunk"))
	resp, err := h.Edge.Caddy.Client(t, H2).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := make([]byte, len("got chunk"))
	if _, err := io.ReadFull(resp.Body, got); err != nil || string(got) != "got chunk" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReloadDoesNotBreakARunningStream(t *testing.T) {
	next := make(chan struct{})
	h := EdgeWithService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			io.WriteString(w, "plain")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		<-next
		fmt.Fprint(w, "data: two\n\n")
	}))
	ce := h.Edge.Caddy
	req, _ := http.NewRequestWithContext(Ctx(t), "GET", "https://app.test/stream", nil)
	resp, err := ce.Client(t, H2).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	br.ReadString('\n')
	br.ReadString('\n')

	ce.Reload(t)
	ce.Reload(t)

	close(next)
	if line, err := br.ReadString('\n'); err != nil || line != "data: two\n" {
		t.Fatalf("after reloads: %q, %v", line, err)
	}
	if _, body := Get(t, ce, H1, "app.test", "/"); body != "plain" {
		t.Fatalf("new request after reloads: %q", body)
	}
}

func TestIdleConnectionsArePooledPerRouteAndDroppedOnCleanup(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.Host) }))
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home"})
	ce := h.Edge.StartCaddy(t, CaddyOptions{})
	h.Edge.Routes.Swap([]routes.Route{
		{Hostname: "one.test", Outpost: o.Name, Service: svc.Addr()},
		{Hostname: "two.test", Outpost: o.Name, Service: svc.Addr()},
	})

	for range 3 {
		Get(t, ce, H2, "one.test", "/")
	}
	if n := svc.Conns(); n != 1 {
		t.Fatalf("3 requests for one Route used %d Service connections, want 1 (pooled)", n)
	}
	Get(t, ce, H2, "two.test", "/")
	if n := svc.Conns(); n != 2 {
		t.Fatalf("a second Route used %d connections in all, want 2 (pooled per Route)", n)
	}

	// A reload runs Cleanup on the old transport, which drops what is idle.
	ce.Reload(t)
	Get(t, ce, H2, "one.test", "/")
	if n := svc.Conns(); n != 3 {
		t.Fatalf("after a reload %d connections, want 3 (idle ones dropped)", n)
	}
}
