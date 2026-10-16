package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
)

func ptr[T any](v T) *T { return &v }

func text(t *testing.T, print func(*bytes.Buffer) error) string {
	t.Helper()
	var b bytes.Buffer
	if err := print(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestSpansUseTheLargestWholeUnit(t *testing.T) {
	for in, want := range map[int64]string{0: "0s", 4: "4s", 59: "59s", 60: "1m", 2520: "42m", 3600: "1h", 86399: "23h", 259200: "3d"} {
		if got := Span(in); got != want {
			t.Errorf("Span(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestOutpostTableShowsEachStateInTheSpecsForms(t *testing.T) {
	l := api.OutpostList{Outposts: []api.OutpostInfo{
		{Name: "fresh", State: api.StateOnline, TunnelAddress: "100.64.0.2", Routes: 0},
		{Name: "nas", State: api.StateOnline, LastSeenAgoSeconds: ptr(int64(4)), Endpoint: "203.0.113.9:51820", Version: "1.2.0", TunnelAddress: "100.64.0.3", Routes: 2},
		{Name: "newbox", State: api.StateNotJoined, TokenExpiresInSeconds: ptr(int64(2520)), TunnelAddress: "100.64.0.4"},
		{Name: "late", State: api.StateNotJoined, TokenExpiresInSeconds: ptr(int64(-5)), TunnelAddress: "100.64.0.5"},
		{Name: "pi", State: api.StateOffline, LastSeenAgoSeconds: ptr(int64(3600)), Version: "1.2.0", TunnelAddress: "100.64.0.6", Routes: 3, DeclaredLines: 3, Declared: true},
		{Name: "old", State: api.StateIncompatible, LastSeenAgoSeconds: ptr(int64(1)), Version: "0.9.0", TunnelAddress: "100.64.0.7"},
	}}
	got := text(t, func(b *bytes.Buffer) error { return Outposts(b, l) })
	for _, sub := range []string{"token expires in 42m", " expired ", "203.0.113.9:51820", "incompatible"} {
		if !strings.Contains(got, sub) {
			t.Errorf("no %q in\n%s", sub, got)
		}
	}
	rows := strings.Split(strings.TrimSpace(got), "\n")
	if len(rows) != 7 || !strings.HasPrefix(rows[0], "NAME") {
		t.Fatalf("rows:\n%s", got)
	}
	// fresh: heard from no one since the restart, so LAST SEEN, ENDPOINT and VERSION are dashes.
	if f := strings.Fields(rows[1]); strings.Join(f, " ") != "fresh online - - - 100.64.0.2 0" {
		t.Errorf("fresh row: %q", rows[1])
	}
	if f := strings.Fields(rows[5]); strings.Join(f, " ") != "pi (declared) offline 1h ago - 1.2.0 100.64.0.6 3 declared" {
		t.Errorf("pi row: %q", rows[5])
	}
}

func TestRouteTableShowsStatusWaitingRowsAndUnknownFields(t *testing.T) {
	l := api.RouteList{Routes: []api.RouteRow{
		{RouteInfo: api.RouteInfo{Hostname: "app.example.com", Outpost: "nas", Service: "http://localhost:3000", Options: []string{"host-header=app.lan", "insecure-skip-verify=true"}}, Status: api.StatusOK},
		{RouteInfo: api.RouteInfo{Hostname: "photos.example.com", Outpost: "nas", Service: "http://localhost:2283"}, Status: api.StatusRenewalFailing},
		{RouteInfo: api.RouteInfo{Hostname: "photos.example.com", Outpost: "newbox", Service: "http://localhost:9000"}, Status: "waiting: taken by nas", Waiting: "taken by nas"},
	}}
	got := text(t, func(b *bytes.Buffer) error { return Routes(b, l) })
	rows := strings.Split(strings.TrimSpace(got), "\n")
	if len(rows) != 4 {
		t.Fatalf("rows:\n%s", got)
	}
	for i, want := range []string{
		"HOSTNAME OUTPOST SERVICE OPTIONS STATUS",
		"app.example.com nas http://localhost:3000 host-header=app.lan insecure-skip-verify=true ok",
		"photos.example.com nas http://localhost:2283 - renewal failing",
		"photos.example.com newbox http://localhost:9000 - waiting: taken by nas",
	} {
		if g := strings.Join(strings.Fields(rows[i]), " "); g != want {
			t.Errorf("row %d: %q, want %q", i, g, want)
		}
	}

	l.Routes[0].Extra = map[string]json.RawMessage{"weight": json.RawMessage("3"), "tag": json.RawMessage(`"blue"`)}
	got = text(t, func(b *bytes.Buffer) error { return Routes(b, l) })
	rows = strings.Split(strings.TrimSpace(got), "\n")
	if g := strings.Join(strings.Fields(rows[0]), " "); !strings.HasSuffix(g, "STATUS MORE") {
		t.Errorf("header %q", g)
	}
	if g := strings.Join(strings.Fields(rows[1]), " "); !strings.HasSuffix(g, "ok tag=blue weight=3") {
		t.Errorf("row %q", g)
	}
}

func TestZoneTableShowsEachWildcardFormAndTheHostnameSummary(t *testing.T) {
	exp := time.Date(2026, 12, 1, 3, 0, 0, 0, time.UTC)
	wild := func(c api.CertInfo) api.ZoneRow {
		return api.ZoneRow{ZoneInfo: api.ZoneInfo{Name: "example.com", Mode: "wildcard", Provider: "cloudflare"}, Certificate: &c}
	}
	for name, tc := range map[string]struct {
		row  api.ZoneRow
		want string
	}{
		"valid":     {wild(api.CertInfo{State: api.CertValid, Expires: &exp}), "valid until 2026-12-01"},
		"obtaining": {wild(api.CertInfo{State: api.CertObtaining}), "obtaining"},
		"renewal":   {wild(api.CertInfo{State: api.CertRenewalFailing, FailingForSeconds: ptr(int64(259200)), Error: "rate limited\nsecond line"}), "renewal failing for 3d: rate limited"},
		"failed":    {wild(api.CertInfo{State: api.CertFailed, Error: "no such zone"}), "failed: no such zone"},
		"expired":   {wild(api.CertInfo{State: api.CertExpired, Expires: &exp}), "expired"},
		"summary": {api.ZoneRow{ZoneInfo: api.ZoneInfo{Name: "home.lan", Mode: "per-hostname"}, Certificates: append(
			make([]api.CertInfo, 11), api.CertInfo{State: api.CertRenewalFailing})}, "12 certificates, 1 failing"},
		"one":  {api.ZoneRow{ZoneInfo: api.ZoneInfo{Name: "home.lan", Mode: "per-hostname"}, Certificates: []api.CertInfo{{State: api.CertValid}}}, "1 certificate"},
		"none": {api.ZoneRow{ZoneInfo: api.ZoneInfo{Name: "home.lan", Mode: "per-hostname"}}, "0 certificates"},
	} {
		if got := ZoneCertificate(tc.row); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
	got := text(t, func(b *bytes.Buffer) error {
		r := wild(api.CertInfo{State: api.CertObtaining})
		r.Declared = true
		return Zones(b, api.ZoneList{Zones: []api.ZoneRow{r}})
	})
	if g := strings.Join(strings.Fields(strings.Split(got, "\n")[1]), " "); g != "example.com (declared) wildcard cloudflare obtaining" {
		t.Errorf("row %q", g)
	}
}
