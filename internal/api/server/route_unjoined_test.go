package server_test

import (
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/edge/liveness"
)

// An Outpost that has not connected yet can't be test-dialed, and the reply
// says so rather than skipping the dial silently.
func TestRouteAddWarnsThatThereWasNoTestDialForAnOutpostThatHasNotConnected(t *testing.T) {
	f := offlineFixture(t, liveness.New(liveness.Config{})) // nas is never seeded: Unknown

	r, err := addRoute(f.client, "app.example.com", "http://localhost:3000", "nas")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings.Warnings) != 1 || !strings.Contains(r.Warnings.Warnings[0], "has not connected yet") || !strings.Contains(r.Warnings.Warnings[0], "no test dial") {
		t.Fatalf("warnings %q", r.Warnings.Warnings)
	}
}
