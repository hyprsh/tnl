package harness

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/render"
)

func edgeStatus(t *testing.T, re *RealEdge) (api.EdgeStatus, string) {
	t.Helper()
	s, err := api.Call[api.EdgeStatus](ctx(t), re.API, http.MethodGet, api.PathStatus, nil)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := render.EdgeStatus(&b, s); err != nil {
		t.Fatal(err)
	}
	return s, b.String()
}

// A healthy Edge prints its summary and that nothing needs attention, which is
// exit 0.
func TestStatusOfAHealthyEdgePrintsTheSummaryAndNothingNeedsAttention(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddOutpost(t, "newbox")
	re.AddRoute(t, photos, "http://"+svc.Addr(), "nas")

	s, text := edgeStatus(t, re)
	if len(s.Attention) != 0 {
		t.Fatalf("attention: %+v", s.Attention)
	}
	if s.Version != harnessVersion || s.Build != "full" || s.Outposts.Online != 1 || s.Outposts.NotJoined != 1 ||
		s.Routes != 1 || s.Zones != 1 || s.StateDir != re.StateDir || s.WireGuardPort == 0 || s.KeyFingerprint == "" {
		t.Errorf("summary: %+v", s)
	}
	for _, want := range []string{"tnl " + harnessVersion + " (full, test)", "Outposts:", "1 online, 1 not joined", "\nNothing needs attention\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in\n%s", want, text)
		}
	}
}

// An Offline Outpost, an Incompatible one and a waiting line each add a line.
func TestStatusNamesAnOfflineOutpostAnIncompatibleOneAndAWaitingLine(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	re := StartEdgeWith(t, EdgeOptions{OfflineAfter: testOfflineAfter})
	re.AddZone(t, "test")
	gone := re.JoinOutpost(t, "gone")
	old, err := re.TryConnectWith(t, "old", re.AddOutpost(t, "old"), mustTempDir(t), ConnectOptions{JoinTimeout: 3 * time.Second, Version: harnessVersion})
	if err != nil {
		t.Fatal(err)
	}
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, photos, "http://"+svc.Addr(), "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "gone")
	re.ConnectWith(t, "newbox", re.AddOutpost(t, "newbox"),
		ConnectOptions{Declaration: []outpostLine{DeclaredLine("PHOTOS", photos, "http://"+svc.Addr())}})
	reconnectAs(t, re, "old", old, "9.0.0")
	gone.Stop()
	waitFor(t, "gone to be Offline", 5*testOfflineAfter, func() bool { return re.Liveness.State("gone") == liveness.Offline })
	waitFor(t, "old to be Incompatible", 5*time.Second, func() bool { return re.Liveness.State("old") == liveness.Incompatible })

	var s api.EdgeStatus
	var text string
	waitFor(t, "the line to wait", 5*time.Second, func() bool {
		s, text = edgeStatus(t, re)
		return len(s.Attention) == 3
	})
	for _, want := range []string{
		"  - Outpost gone is offline, last seen ",
		", 1 Route\n",
		"  - Outpost old is incompatible, last seen ",
		"  - newbox declares " + photos + ", which nas holds\n",
		"\nNeeds attention\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "Nothing needs attention") {
		t.Errorf("text:\n%s", text)
	}
}
