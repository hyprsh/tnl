package harness

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/outpost"
	"github.com/hyprsh/tnl/internal/remind"
	"github.com/hyprsh/tnl/internal/render"
)

func outpostStatus(t *testing.T, j *JoinedOutpost) (api.OutpostStatus, string) {
	t.Helper()
	s, err := api.Call[api.OutpostStatus](Ctx(t), j.CLI, http.MethodGet, api.PathStatus, nil)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := render.OutpostStatus(&b, s, time.Now()); err != nil {
		t.Fatal(err)
	}
	return s, b.String()
}

func mustContain(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in\n%s", want, text)
		}
	}
}

// A healthy Outpost shows both sides, its Routes and its declared lines, and
// nothing needs attention, which is exit 0.
func TestStatusOfAHealthyOutpostShowsBothSides(t *testing.T) {
	t.Parallel()
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	nas := re.ConnectWith(t, "nas", re.AddOutpost(t, "nas"), ConnectOptions{
		Declaration: []OutpostLine{DeclaredLine("PHOTOS", Photos, "http://"+svc.Addr())},
	})
	var s api.OutpostStatus
	var text string
	WaitFor(t, "the line to apply", 5*time.Second, func() bool {
		s, text = outpostStatus(t, nas)
		return len(s.Routes) == 1
	})
	if len(s.Attention) != 0 || s.Name != "nas" || s.Version != HarnessVersion || s.Edge.State != api.TunnelUp ||
		s.Edge.Version != HarnessVersion || s.Edge.LastAnswer == nil || s.StateDir != nas.StateDir || s.TunnelAddress == "" ||
		s.Edge.Address != "127.0.0.1" || s.Edge.IP != "127.0.0.1" || s.Routes[0].Hostname != Photos {
		t.Errorf("status: %+v", s)
	}
	mustContain(t, text, "Outpost nas, tnl "+HarnessVersion+" (outpost, test)", "Tunnel:           up", "Routes\n  "+Photos,
		"Declared\n  TNL_ROUTE_PHOTOS: "+Photos, "\nNothing needs attention\n")
}

// With the Edge stopped the relay still answers: no answer, Routes unavailable.
func TestStatusOfAnOutpostWhoseEdgeIsStoppedSaysSo(t *testing.T) {
	re := StartEdgeWith(t, EdgeOptions{})
	nas := re.ConnectWith(t, "nas", re.AddOutpost(t, "nas"), ConnectOptions{
		NoAnswerAfter: 400 * time.Millisecond, Reminders: remind.Schedule{Every: time.Hour},
	})
	re.Stop()
	var s api.OutpostStatus
	var text string
	WaitFor(t, "the Tunnel to count as down", 10*time.Second, func() bool {
		s, text = outpostStatus(t, nas)
		return s.Edge.State == api.TunnelDown
	})
	if s.Routes != nil || s.RoutesUnavailable != outpost.RoutesUnavailable || len(s.Attention) != 1 || s.Attention[0].Kind != api.AttentionTunnelDown {
		t.Errorf("status: %+v", s)
	}
	mustContain(t, text, "Tunnel:           no answer for ", "Routes: unavailable while the Tunnel is down", "Needs attention\n  - no answer from the Edge for ")
}

// A waiting declared line needs attention, and says why.
func TestStatusOfAnOutpostWithAWaitingLineNeedsAttention(t *testing.T) {
	t.Parallel()
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, Photos, "http://"+svc.Addr(), "nas")
	newbox := re.ConnectWith(t, "newbox", re.AddOutpost(t, "newbox"),
		ConnectOptions{Declaration: []OutpostLine{DeclaredLine("PHOTOS", Photos, "http://"+svc.Addr())}})
	var s api.OutpostStatus
	var text string
	WaitFor(t, "the line to wait", 5*time.Second, func() bool {
		s, text = outpostStatus(t, newbox)
		return len(s.Attention) == 1
	})
	if s.Attention[0].Kind != api.AttentionDeclaredWaiting || len(s.Declared) != 1 || !s.Declared[0].Waiting || s.Declared[0].Reason != "taken by nas" {
		t.Errorf("status: %+v", s)
	}
	mustContain(t, text, "(waiting: taken by nas)", "Needs attention\n  - "+Photos+" waits: taken by nas\n")
}

// An Edge that says the Outpost is Incompatible needs attention, and the
// relay says it in the Edge's words.
func TestStatusOfAnIncompatibleOutpostNeedsAttention(t *testing.T) {
	re := StartEdgeWith(t, EdgeOptions{Version: "0.3.4", OfflineAfter: ShortOfflineAfter})
	nas, err := re.TryConnectWith(t, "nas", re.AddOutpost(t, "nas"), MustTempDir(t), ConnectOptions{JoinTimeout: 3 * time.Second, Version: "0.3.1"})
	if err != nil {
		t.Fatal(err)
	}
	nas = reconnectAs(t, re, "nas", nas, "0.4.0")
	WaitFor(t, "nas to be Incompatible", 5*time.Second, func() bool { return re.Liveness.State("nas") == liveness.Incompatible })
	var s api.OutpostStatus
	WaitFor(t, "the Outpost to hear it", 5*time.Second, func() bool {
		s, _ = outpostStatus(t, nas)
		return s.Edge.Incompatible
	})
	if len(s.Attention) != 1 || s.Attention[0].Kind != api.AttentionEdgeIncompatible || !strings.Contains(s.Edge.Message, "doesn't accept this Outpost") {
		t.Errorf("status: %+v", s)
	}
}
