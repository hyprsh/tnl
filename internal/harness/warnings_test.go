package harness

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
)

type nowhere struct{}

func (nowhere) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if host == "127.0.0.1" {
		return nil, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// A wildcard Zone whose names resolve nowhere is saved, the reply carries the
// warning and the real Edge logs it at warn with the actor.
func TestRealEdgeWildcardZoneAddWarnsAndLogs(t *testing.T) {
	StartFakeDNS(t, "example.test", dnsToken)
	log := new(lockedBuf)
	e := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), Resolver: nowhere{}, LogOut: log, LogLevel: slog.LevelWarn})
	z, err := zoneAddWildcard(t, e, "example.test", "fake", dnsToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(z.Warnings.Warnings) != 1 {
		t.Fatalf("warnings %q", z.Warnings.Warnings)
	}
	if len(e.Store.Snapshot().Zones) != 1 {
		t.Fatal("the Zone was not saved")
	}
	var line string
	for _, l := range strings.Split(log.String(), "\n") {
		if strings.Contains(l, "level=warn") && strings.Contains(l, "actor=local") {
			line = l
		}
	}
	if line == "" || !strings.Contains(line, "doesn't resolve") {
		t.Fatalf("no warn line with the actor in:\n%s", log.String())
	}
}

// With the default resolver every name resolves to the Edge: no warning.
func TestRealEdgeZoneAddNoWarningWhenNamesPointAtTheEdge(t *testing.T) {
	e := StartEdge(t)
	z, err := api.Call[api.ZoneAddReply](context.Background(), e.API, http.MethodPost, api.PathZones, api.ZoneAddRequest{Name: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if z.Warnings.Warnings != nil {
		t.Fatalf("warnings %q", z.Warnings.Warnings)
	}
}

// A per-hostname Zone needs only each Hostname to point at the Edge, so a
// wildcard record pointing elsewhere is no reason to warn.
func TestRealEdgePerHostnameZoneAddGivesNoDNSWarning(t *testing.T) {
	log := new(lockedBuf)
	e := StartEdgeWith(t, EdgeOptions{Resolver: nowhere{}, LogOut: log, LogLevel: slog.LevelWarn})
	z, err := api.Call[api.ZoneAddReply](context.Background(), e.API, http.MethodPost, api.PathZones, api.ZoneAddRequest{Name: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if z.Warnings.Warnings != nil || strings.Contains(log.String(), "level=warn") {
		t.Fatalf("warnings %q, log:\n%s", z.Warnings.Warnings, log)
	}
}
