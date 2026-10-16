package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
)

func TestEdgeStatusPrintsTheSummaryThenWhatNeedsAttention(t *testing.T) {
	s := api.EdgeStatus{
		Role: api.RoleEdge, Version: "1.2.0", Build: "full", Revision: "abc1234", UptimeSeconds: 93600,
		Address: "edge.example.com", WireGuardPort: 51820, KeyFingerprint: "SHA256:abc", StateDir: "/var/lib/tnl",
		LogLevel: "info", AccessLog: true, Outposts: api.OutpostCounts{Online: 2, Offline: 1}, Routes: 5, Zones: 2,
		Certificates: api.CertSummary{Count: 4, SoonestExpiry: ptr(time.Date(2026, 12, 1, 3, 0, 0, 0, time.UTC))},
	}
	healthy := text(t, func(b *bytes.Buffer) error { return EdgeStatus(b, s) })
	want := `tnl 1.2.0 (full, abc1234)
Uptime:           1d
Address:          edge.example.com, WireGuard port 51820
Key:              SHA256:abc
State directory:  /var/lib/tnl
Log:              level info, access log on
Outposts:         2 online, 1 offline
Routes:           5
Zones:            2
Certificates:     4, the soonest expires 2026-12-01

Nothing needs attention
`
	if healthy != want {
		t.Errorf("got:\n%s\nwant:\n%s", healthy, want)
	}
	s.Attention = []api.AttentionItem{{Message: "Outpost pi is offline"}, {Message: "x declares y, which nas holds"}}
	sick := text(t, func(b *bytes.Buffer) error { return EdgeStatus(b, s) })
	if !strings.HasSuffix(sick, "\nNeeds attention\n  - Outpost pi is offline\n  - x declares y, which nas holds\n") || strings.Contains(sick, "Nothing") {
		t.Errorf("got:\n%s", sick)
	}
}
