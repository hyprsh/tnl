package api

import (
	"testing"
	"time"
)

func TestEdgeStatusJSONIsPinned(t *testing.T) {
	golden(t, "status_edge", EdgeStatus{
		Role: RoleEdge, Version: "1.2.0", Build: "full", Revision: "abc1234",
		Started: t0.Add(-26 * time.Hour), UptimeSeconds: 93600,
		Address: "edge.example.com", WireGuardPort: 51820, KeyFingerprint: "SHA256:abcdefgh",
		StateDir: "/var/lib/tnl", LogLevel: "info", AccessLog: true,
		Outposts: OutpostCounts{Online: 2, Offline: 1, Incompatible: 1, NotJoined: 1},
		Routes:   5, Zones: 2,
		Certificates: CertSummary{Count: 4, SoonestExpiry: ptr(t0.Add(72 * time.Hour))},
		Attention: []AttentionItem{
			{Kind: AttentionOutpostOffline, Subject: "pi", Message: "Outpost pi is offline, last seen 1h ago, 1 Route"},
			{Kind: AttentionCertificateExpiring, Subject: "a.home.lan", Message: "certificate a.home.lan expires in 3d"},
			{Kind: AttentionLineWaiting, Subject: "photos.example.com", Outpost: "newbox",
				Message: "newbox declares photos.example.com, which nas holds"},
		},
	})
}

func TestHealthyEdgeStatusJSONIsPinned(t *testing.T) {
	golden(t, "status_edge_healthy", EdgeStatus{
		Role: RoleEdge, Version: "1.2.0", Build: "full",
		Started: t0, Address: "edge.example.com", WireGuardPort: 51820, KeyFingerprint: "SHA256:abcdefgh",
		StateDir: "/var/lib/tnl", LogLevel: "info", Attention: []AttentionItem{},
	})
}
