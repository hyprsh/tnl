package harness

import (
	"bufio"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
)

// openStream opens an event stream on host and returns a func that reads the
// next event, reporting whether one arrived before the stream ended.
func openStream(t *testing.T, re *RealEdge, host string) (next func() bool) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx(t), "GET", "https://"+host+"/sse", nil)
	resp, err := re.Caddy.Client(t, H1).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	br := bufio.NewReader(resp.Body)
	return func() bool {
		line, err := br.ReadString('\n')
		return err == nil && line != ""
	}
}

func routeRm(t *testing.T, re *RealEdge, hostname string) error {
	_, err := api.Call[api.RouteRmReply](ctx(t), re.API, http.MethodPost, api.PathRouteRm, api.RouteRmRequest{Hostname: hostname})
	return err
}

func TestRouteRmEndsItsStreamsAndLeavesOtherRoutesFlowing(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sse" {
			fmt.Fprint(w, "ok")
			return
		}
		sseHandler(w, r)
	}))
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "a.test", "http://"+svc.Addr(), "nas")
	re.AddRoute(t, "b.test", "http://"+svc.Addr(), "nas")
	// Pay for both certificates first.
	get(t, re.Caddy, H1, "a.test", "/")
	get(t, re.Caddy, H1, "b.test", "/")

	nextA := openStream(t, re, "a.test")
	nextB := openStream(t, re, "b.test")
	if !nextA() || !nextB() {
		t.Fatal("a stream did not start")
	}
	// A second connection for a.test, left idle in the pool: get while the
	// stream holds the first.
	get(t, re.Caddy, H1, "a.test", "/")
	waitForLive(t, svc, 3)

	if err := routeRm(t, re, "a.test"); err != nil {
		t.Fatal(err)
	}

	// a.test's stream ends; drain what was in flight.
	ended := false
	for i := 0; i < 1000 && !ended; i++ {
		ended = !nextA()
	}
	if !ended {
		t.Fatal("a.test's stream is still flowing after route rm")
	}
	for range 2 {
		if !nextB() {
			t.Fatal("b.test's stream stopped")
		}
	}
	// Its idle pooled connection is closed too: only b.test's stream is left.
	waitForLive(t, svc, 1)
	if resp, _ := get(t, re.Caddy, H1, "b.test", "/"); resp.StatusCode != 200 {
		t.Fatalf("b.test answered %d", resp.StatusCode)
	}
}

func TestRouteRmHostnameAnswers404AndUnknownIsRefused(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "a.test", "http://"+svc.Addr(), "nas")
	if resp, _ := get(t, re.Caddy, H1, "a.test", "/"); resp.StatusCode != 200 {
		t.Fatalf("got %d", resp.StatusCode)
	}
	if err := routeRm(t, re, "a.test"); err != nil {
		t.Fatal(err)
	}
	if resp, _ := get(t, re.Caddy, H1, "a.test", "/"); resp.StatusCode != 404 {
		t.Fatalf("after route rm: got %d, want 404", resp.StatusCode)
	}
	err := routeRm(t, re, "a.test")
	if e, ok := err.(*api.Error); !ok || e.Status != http.StatusNotFound {
		t.Fatalf("removing an unknown Hostname: %v", err)
	}
}

// sseHandler streams events until the Visitor or the connection goes away.
func sseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	rc := http.NewResponseController(w)
	for {
		if _, err := fmt.Fprint(w, "data: x\n\n"); err != nil || rc.Flush() != nil {
			return
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
	}
}

func waitForLive(t *testing.T, svc *Service, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for svc.Live() != n {
		if time.Now().After(deadline) {
			t.Fatalf("the Service has %d open connections, want %d", svc.Live(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
