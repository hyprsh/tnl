package caddyx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

// A Service that sends a header wins over the Route's, whatever the value,
// including an empty one.
func TestServiceHeaderWinsEvenWhenEmpty(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header()["X-Frame-Options"] = []string{""}
	rec := &recorder{ResponseWriter: w, headers: []routes.Header{{Name: "X-Frame-Options", Value: "DENY"}, {Name: "X-Extra", Value: "1"}}}
	rec.WriteHeader(http.StatusOK)
	if got := w.Header()["X-Frame-Options"]; len(got) != 1 || got[0] != "" {
		t.Errorf("the Service's empty header was replaced: %q", got)
	}
	if got := w.Header().Get("X-Extra"); got != "1" {
		t.Errorf("X-Extra = %q", got)
	}
}
