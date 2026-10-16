package render

import (
	"bytes"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
)

func TestOutpostStatusPrintsBothSidesAndWhatNeedsAttention(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	s := api.OutpostStatus{
		Role: api.RoleOutpost, Name: "nas", Version: "1.2.0", Build: "outpost", Revision: "abc1234",
		TunnelAddress: "10.99.0.2", StateDir: "/var/lib/tnl", Allow: "192.168.1.0/24",
		Edge:      api.OutpostEdge{Address: "edge.example.com", IP: "203.0.113.7", State: api.TunnelUp, LastAnswer: ptr(now.Add(-4 * time.Second)), Version: "1.2.0"},
		Routes:    []api.OutpostRoute{{Hostname: "media.example.com", Service: "http://nas:8096"}},
		Declared:  []api.DeclaredLineStatus{{Hostname: "media.example.com", Service: "http://nas:8096", Variable: "TNL_ROUTE_MEDIA"}},
		Attention: []api.AttentionItem{},
	}
	got := text(t, func(b *bytes.Buffer) error { return OutpostStatus(b, s, now) })
	want := `Outpost nas, tnl 1.2.0 (outpost, abc1234)
Tunnel address:   10.99.0.2
State directory:  /var/lib/tnl
Allow:            192.168.1.0/24
Edge:             edge.example.com (203.0.113.7)
Tunnel:           up
Last answer:      4s ago
Edge version:     1.2.0

Routes
  media.example.com -> http://nas:8096

Declared
  TNL_ROUTE_MEDIA: media.example.com -> http://nas:8096

Nothing needs attention
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	s.Edge.State, s.Edge.SilentSeconds = api.TunnelDown, 240
	s.Edge.Incompatible, s.Edge.Message = true, "the Edge doesn't accept this Outpost"
	s.Routes, s.RoutesUnavailable = nil, "unavailable while the Tunnel is down"
	s.Declared[0].Waiting, s.Declared[0].Reason = true, "taken by newbox"
	s.Attention = []api.AttentionItem{{Message: "no answer from the Edge for 4m"}}
	got = text(t, func(b *bytes.Buffer) error { return OutpostStatus(b, s, now) })
	want = `Outpost nas, tnl 1.2.0 (outpost, abc1234)
Tunnel address:   10.99.0.2
State directory:  /var/lib/tnl
Allow:            192.168.1.0/24
Edge:             edge.example.com (203.0.113.7)
Tunnel:           no answer for 4m
Last answer:      4s ago
Edge version:     1.2.0, the Edge doesn't accept this Outpost

Routes: unavailable while the Tunnel is down

Declared
  TNL_ROUTE_MEDIA: media.example.com -> http://nas:8096 (waiting: taken by newbox)

Needs attention
  - no answer from the Edge for 4m
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
