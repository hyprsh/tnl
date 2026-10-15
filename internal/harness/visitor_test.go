package harness

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

// get makes a GET to https://host/path as a Visitor on proto.
func get(t *testing.T, ce *CaddyEdge, proto Proto, host, path string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx(t), "GET", "https://"+host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ce.Client(t, proto).Do(req)
	if err != nil {
		t.Fatalf("%s: %v", proto, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s: reading body: %v", proto, err)
	}
	return resp, string(b)
}

// edgeWithService starts an Edge with Caddy, one Outpost and a Route from
// app.test to a Service running handler.
func edgeWithService(t *testing.T, handler http.Handler) *Harness {
	t.Helper()
	svc := NewHTTPService(t, handler)
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home"})
	h.Edge.StartCaddy(t, CaddyOptions{})
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "app.test", Outpost: o.Name, Service: svc.Addr()}})
	return h
}

func TestVisitorRequestReachesTheService(t *testing.T) {
	h := edgeWithService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "service saw %s %s", r.Method, r.URL.Path)
	}))
	for _, proto := range Protos {
		t.Run(string(proto), func(t *testing.T) {
			resp, body := get(t, h.Edge.Caddy, proto, "app.test", "/hello")
			if resp.StatusCode != 200 || body != "service saw GET /hello" {
				t.Fatalf("got %d %q", resp.StatusCode, body)
			}
			if resp.Proto != string(proto) {
				t.Fatalf("Visitor spoke %s, want %s", resp.Proto, proto)
			}
		})
	}
}
