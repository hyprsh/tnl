package outposts

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/allow"
	"github.com/hyprsh/tnl/internal/api"

	. "github.com/hyprsh/tnl/internal/harness"
)

func routeEditWarnings(t *testing.T, re *RealEdge, req api.RouteEditRequest) []string {
	t.Helper()
	r, err := api.Call[api.RouteEditReply](Ctx(t), re.API, http.MethodPost, api.PathRouteEdit, req)
	if err != nil {
		t.Fatal(err)
	}
	return r.Warnings.Warnings
}

func hasWarning(ws []string, parts ...string) bool {
	for _, w := range ws {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(w, p)
		}
		if ok {
			return true
		}
	}
	return false
}

func TestRouteAddForAServiceThatRefusesSavesTheRouteAndWarnsAtWarn(t *testing.T) {
	log := new(LockedBuf)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), LogOut: log, LogLevel: slog.LevelWarn})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	addr := ClosedAddr(t)

	r := re.AddRouteReply(t, "app.test", "http://"+addr, "nas")
	if !hasWarning(r.Warnings.Warnings, addr, "doesn't answer", "refused") {
		t.Fatalf("warnings %q", r.Warnings.Warnings)
	}
	if len(re.Store.Snapshot().Routes) != 1 {
		t.Fatal("the Route was not saved")
	}
	var line string
	for _, l := range strings.Split(log.String(), "\n") {
		if strings.Contains(l, "level=warn") && strings.Contains(l, "doesn't answer") && strings.Contains(l, "actor=local") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no warn line in:\n%s", log.String())
	}

	// A Service that answers draws no warning.
	svc := NewHTTPService(t, EchoHost())
	if r := re.AddRouteReply(t, "ok.test", "http://"+svc.Addr(), "nas"); len(r.Warnings.Warnings) != 0 {
		t.Fatalf("warnings %q", r.Warnings.Warnings)
	}
}

func TestVerifiedHTTPSServiceWithABadCertificateWarnsWithTheHint(t *testing.T) {
	trusted := NewHTTPSService(t, EchoHost())
	untrusted := NewHTTPSService(t, EchoHost())
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), ServiceRoots: trusted.CA})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")

	if r := re.AddRouteReply(t, "ok.test", "https://"+trusted.Addr(), "nas"); len(r.Warnings.Warnings) != 0 {
		t.Fatalf("trusted certificate: warnings %q", r.Warnings.Warnings)
	}
	r := re.AddRouteReply(t, "bad.test", "https://"+untrusted.Addr(), "nas")
	if !hasWarning(r.Warnings.Warnings, "certificate", "--insecure-skip-verify") {
		t.Fatalf("warnings %q", r.Warnings.Warnings)
	}
	skip := func(r *api.RouteAddRequest) { r.InsecureSkipVerify = true }
	if r := re.AddRouteReply(t, "skip.test", "https://"+untrusted.Addr(), "nas", skip); len(r.Warnings.Warnings) != 0 {
		t.Fatalf("with --insecure-skip-verify: warnings %q", r.Warnings.Warnings)
	}
}

func TestRouteEditTestDialsOnlyWhenTheServiceChanged(t *testing.T) {
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRouteReply(t, "app.test", "http://"+ClosedAddr(t), "nas")

	if ws := routeEditWarnings(t, re, api.RouteEditRequest{Hostname: "app.test", HostHeader: ptr("internal.lan")}); len(ws) != 0 {
		t.Fatalf("an option-only edit test-dialed: %q", ws)
	}
	same := "http://" + re.Store.Snapshot().Routes[0].Service[len("http://"):]
	if ws := routeEditWarnings(t, re, api.RouteEditRequest{Hostname: "app.test", Service: &same}); len(ws) != 0 {
		t.Fatalf("an edit that kept the Service test-dialed: %q", ws)
	}
	other := "http://" + ClosedAddr(t)
	if ws := routeEditWarnings(t, re, api.RouteEditRequest{Hostname: "app.test", Service: &other}); !hasWarning(ws, "doesn't answer") {
		t.Fatalf("a changed Service did not warn: %q", ws)
	}
}

func TestAllowlistRefusesAServiceOutsideItAndTheRequestIsNotRetried(t *testing.T) {
	svc := NewHTTPService(t, EchoHost())
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	list, err := allow.Parse("192.168.1.0/24,localhost:3000")
	if err != nil {
		t.Fatal(err)
	}
	outLog := new(LockedBuf)
	tok := re.AddOutpost(t, "nas")
	re.ConnectWith(t, "nas", tok, ConnectOptions{LogOut: outLog, Allow: list})
	if !strings.Contains(outLog.String(), "allow=192.168.1.0/24,localhost:3000") {
		t.Fatalf("the Outpost did not log its allowlist at start:\n%s", outLog.String())
	}

	r := re.AddRouteReply(t, "app.test", "http://"+svc.Addr(), "nas")
	if !hasWarning(r.Warnings.Warnings, "allowlist", svc.Addr()) {
		t.Fatalf("warnings %q", r.Warnings.Warnings)
	}
	if len(re.Store.Snapshot().Routes) != 1 {
		t.Fatal("the Route was not saved")
	}

	refusals := func() int { return strings.Count(outLog.String(), "allowlist refused") }
	before := refusals()
	if resp, _ := Get(t, re.Caddy, H1, "app.test", "/"); resp.StatusCode != 502 {
		t.Fatalf("got %d, want 502", resp.StatusCode)
	}
	if n := refusals() - before; n != 1 {
		t.Fatalf("%d refusals for one request, want 1 (no retry):\n%s", n, outLog.String())
	}
	var line string
	for _, l := range strings.Split(outLog.String(), "\n") {
		if strings.Contains(l, "allowlist refused") {
			line = l
		}
	}
	if !strings.Contains(line, "level=INFO") && !strings.Contains(line, "level=info") {
		t.Fatalf("not an info line: %s", line)
	}
	if !strings.Contains(line, " id=") || !strings.Contains(line, "service="+svc.Addr()) {
		t.Fatalf("line lacks the request ID or the address: %s", line)
	}
}
