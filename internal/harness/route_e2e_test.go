package harness

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/edge/caddyx"
)

// The tracer bullet: a Zone, an Outpost that joined by token and a Route added
// through the API make a Service answer a Visitor over every HTTP version, with
// a certificate from Pebble.
func TestRouteAddMakesAServiceAnswerOverEveryHTTPVersion(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello from "+r.Host)
	}))
	pb := StartPebble(t, PebbleOptions{})
	re := StartEdgeWith(t, EdgeOptions{Pebble: pb})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")

	for _, proto := range Protos {
		resp, body := Get(t, re.Caddy, proto, "app.test", "/")
		if resp.StatusCode != 200 || body != "hello from app.test" {
			t.Fatalf("%s: got %d %q", proto, resp.StatusCode, body)
		}
		if _, err := resp.TLS.PeerCertificates[0].Verify(verifyOpts(pb, resp.TLS.PeerCertificates[1:])); err != nil {
			t.Errorf("%s: certificate is not Pebble's: %v", proto, err)
		}
	}
	if pb.Orders() != 1 {
		t.Errorf("Pebble saw %d orders, want 1", pb.Orders())
	}
}

// A new Route is swapped into the router's table. Caddy is not loaded again, so
// a stream on another Route is never touched.
func TestAddingARouteDoesNotReloadCaddyAndLeavesAnOpenEventStreamUp(t *testing.T) {
	svc := NewHTTPService(t, suiteService())
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")

	// The first request pays for the certificate (the wait at `route add` comes
	// in slice 5), which an HTTP/3 handshake would not outlast.
	Get(t, re.Caddy, H1, "app.test", "/nope")
	loads := caddyx.Loads()
	done := make(chan map[Proto]sseRun, 1)
	go func() {
		done <- forEachProto(func(p Proto) sseRun { return readSSE(t, re.Caddy, p, "1s,1s,1s,1s,1s,1s,1s") })
	}()
	// Let the streams open, then add Routes while they run.
	waitForConns(t, svc, len(Protos)) // the warm-up's pooled connection carries one stream
	re.AddRoute(t, "two.test", "http://"+svc.Addr(), "nas")
	// This request waits for the new Hostname's certificate, so the streams
	// outlive it. The Service has no /nope, so its 404 shows the Route works.
	if resp, body := Get(t, re.Caddy, H1, "two.test", "/nope"); resp.StatusCode != 404 {
		t.Fatalf("two.test: got %d %q", resp.StatusCode, body)
	}
	res := <-done
	for _, p := range Protos {
		if r := res[p]; r.err != nil || r.events != 8 {
			t.Errorf("%s: %d of 8 events, %v", p, r.events, r.err)
		}
	}
	if n := caddyx.Loads(); n != loads {
		t.Errorf("Caddy was loaded %d more times while Routes were added", n-loads)
	}
}

func TestRoutesSurviveAnEdgeRestart(t *testing.T) {
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	re.AddOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://localhost:3000", "nas")
	re.Stop()

	again := StartEdgeWith(t, EdgeOptions{StateDir: re.StateDir, Pebble: StartPebble(t, PebbleOptions{})})
	list, err := api.Call[api.RouteList](Ctx(t), again.API, http.MethodGet, api.PathRoutes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Routes) != 1 || list.Routes[0].Hostname != "app.test" || list.Routes[0].Service != "http://localhost:3000" {
		t.Fatalf("%+v", list)
	}
}

func waitForConns(t *testing.T, svc *Service, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for svc.Conns() < n {
		if time.Now().After(deadline) {
			t.Fatalf("the Service saw %d connections, want %d", svc.Conns(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
