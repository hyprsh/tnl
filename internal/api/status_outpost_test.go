package api

import (
	"testing"
	"time"
)

func TestOutpostStatusJSONIsPinned(t *testing.T) {
	golden(t, "status_outpost", OutpostStatus{
		Role: RoleOutpost, Name: "nas", Version: "1.2.0", Build: "outpost", Revision: "abc1234",
		TunnelAddress: "10.99.0.2", StateDir: "/var/lib/tnl", Allow: "192.168.1.0/24",
		Edge: OutpostEdge{Address: "edge.example.com", IP: "203.0.113.7", State: TunnelDown,
			LastAnswer: ptr(t0.Add(-4 * time.Minute)), SilentSeconds: 240, Version: "1.3.0",
			Incompatible: true, Message: "the Edge (1.3.0) doesn't accept this Outpost (1.2.0); upgrade the Outpost"},
		RoutesUnavailable: "unavailable while the Tunnel is down",
		Declared: []DeclaredLineStatus{
			{Hostname: "media.example.com", Service: "http://nas:8096", Variable: "TNL_ROUTE_MEDIA"},
			{Hostname: "photos.example.com", Service: "http://nas:2342", Variable: "TNL_ROUTE_PHOTOS", Waiting: true, Reason: "taken by newbox"},
		},
		Attention: []AttentionItem{{Kind: AttentionTunnelDown, Subject: "edge.example.com", Message: "no answer from the Edge for 4m"}},
	})
}

func TestHealthyOutpostStatusJSONIsPinned(t *testing.T) {
	golden(t, "status_outpost_healthy", OutpostStatus{
		Role: RoleOutpost, Name: "nas", Version: "1.2.0", Build: "outpost",
		TunnelAddress: "10.99.0.2", StateDir: "/var/lib/tnl",
		Edge:      OutpostEdge{Address: "edge.example.com", IP: "203.0.113.7", State: TunnelUp, LastAnswer: ptr(t0), Version: "1.2.0"},
		Routes:    []OutpostRoute{{Hostname: "media.example.com", Service: "http://nas:8096"}},
		Attention: []AttentionItem{},
	})
}
