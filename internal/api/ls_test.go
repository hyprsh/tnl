package api

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// golden compares v's indented JSON, the form `--json` prints, with the file
// in testdata. These files are the frozen shapes: change one only by adding.
func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name+".golden.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs from %s (run with -update to accept)\ngot:\n%s\nwant:\n%s", name, path, got, want)
	}
}

func ptr[T any](v T) *T { return &v }

var (
	t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
)

func TestOutpostListJSONIsPinned(t *testing.T) {
	golden(t, "outpost_ls", OutpostList{Outposts: []OutpostInfo{
		{
			Name: "nas", State: StateOnline, LastSeen: ptr(t0), LastSeenAgoSeconds: ptr(int64(4)),
			Endpoint: "203.0.113.9:51820", Version: "1.2.0", TunnelAddress: "100.64.0.2", Routes: 2,
		},
		{
			Name: "newbox", State: StateNotJoined, TokenExpires: ptr(t0.Add(42 * time.Minute)),
			TokenExpiresInSeconds: ptr(int64(2520)), TunnelAddress: "100.64.0.3",
		},
		{
			Name: "old", State: StateIncompatible, LastSeen: ptr(t0), LastSeenAgoSeconds: ptr(int64(1)),
			Endpoint: "198.51.100.7:4000", Version: "0.9.0", TunnelAddress: "100.64.0.4",
		},
		{
			Name: "pi", State: StateOffline, LastSeen: ptr(t0.Add(-time.Hour)), LastSeenAgoSeconds: ptr(int64(3600)),
			Version: "1.2.0", TunnelAddress: "100.64.0.5", Routes: 1, DeclaredLines: 3, Declared: true,
		},
	}})
}

func TestRouteListJSONIsPinned(t *testing.T) {
	golden(t, "route_ls", RouteList{Routes: []RouteRow{
		{
			RouteInfo: RouteInfo{Hostname: "app.example.com", Outpost: "nas", Service: "http://localhost:3000", Options: []string{"host-header=app.lan"}},
			Status:    StatusOK,
		},
		{
			RouteInfo: RouteInfo{Hostname: "old.example.com", Outpost: "pi", Service: "http://localhost:80"},
			Status:    StatusOutpostOffline,
		},
		{
			RouteInfo: RouteInfo{Hostname: "photos.example.com", Outpost: "nas", Service: "http://localhost:2283"},
			Status:    StatusRenewalFailing,
		},
		{
			RouteInfo: RouteInfo{Hostname: "photos.example.com", Outpost: "newbox", Service: "http://localhost:9000"},
			Status:    StatusWaiting(WaitHeld.Words("nas")), Waiting: WaitHeld.Words("nas"),
		},
		{
			RouteInfo: RouteInfo{Hostname: "x.other.org", Outpost: "newbox", Service: "http://localhost:9001"},
			Status:    StatusWaiting(WaitOutsideZone.Words("")), Waiting: WaitOutsideZone.Words(""),
		},
	}})
}

func TestZoneListJSONIsPinned(t *testing.T) {
	golden(t, "zone_ls", ZoneList{Zones: []ZoneRow{
		{
			ZoneInfo: ZoneInfo{Name: "example.com", Mode: "wildcard", Provider: "cloudflare"}, Declared: true,
			Certificate: &CertInfo{Name: "*.example.com", State: CertRenewalFailing, Expires: ptr(t0.Add(720 * time.Hour)),
				FailingForSeconds: ptr(int64(259200)), Error: "acme: error presenting token:\nrate limited"},
		},
		{
			ZoneInfo: ZoneInfo{Name: "home.lan", Mode: "per-hostname"},
			Certificates: []CertInfo{
				{Name: "a.home.lan", State: CertValid, Expires: ptr(t0.Add(1440 * time.Hour))},
				{Name: "b.home.lan", State: CertObtaining},
			},
		},
	}})
}

// A client that is older than the Edge still lists its Routes: fields it does
// not know come out in Extra, and --json passes them on.
func TestARouteRowKeepsTheFieldsItDoesNotKnow(t *testing.T) {
	in := `{"hostname":"a.example.com","outpost":"nas","service":"http://x","status":"ok","weight":3,"tag":"blue"}`
	var row RouteRow
	if err := json.Unmarshal([]byte(in), &row); err != nil {
		t.Fatal(err)
	}
	if row.Hostname != "a.example.com" || row.Status != "ok" {
		t.Fatalf("known fields lost: %+v", row)
	}
	if len(row.Extra) != 2 || string(row.Extra["weight"]) != "3" || string(row.Extra["tag"]) != `"blue"` {
		t.Fatalf("extra: %v", row.Extra)
	}
	out, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"hostname":"a.example.com","outpost":"nas","service":"http://x","status":"ok","tag":"blue","weight":3}`
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
	// And a strict decode of a list still accepts it.
	var list RouteList
	dec := json.NewDecoder(bytes.NewReader([]byte(`{"routes":[` + in + `]}`)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&list); err != nil || len(list.Routes) != 1 {
		t.Fatalf("strict decode: %v %+v", err, list)
	}
}
