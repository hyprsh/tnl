package harness

import (
	"fmt"
	"net/http"
	"testing"
)

func TestVisitorRequestReachesTheService(t *testing.T) {
	h := EdgeWithService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "service saw %s %s", r.Method, r.URL.Path)
	}))
	for _, proto := range Protos {
		t.Run(string(proto), func(t *testing.T) {
			resp, body := Get(t, h.Edge.Caddy, proto, "app.test", "/hello")
			if resp.StatusCode != 200 || body != "service saw GET /hello" {
				t.Fatalf("got %d %q", resp.StatusCode, body)
			}
			if resp.Proto != string(proto) {
				t.Fatalf("Visitor spoke %s, want %s", resp.Proto, proto)
			}
		})
	}
}
