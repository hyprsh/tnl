package harness

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/edge/caddyx"
)

const dnsToken = "cf-T0KEN-0123456789abcdef"

func zoneAddWildcard(t *testing.T, re *RealEdge, name, provider, token string) (api.ZoneAddReply, error) {
	t.Helper()
	return api.Call[api.ZoneAddReply](ctx(t), re.API, http.MethodPost, api.PathZones, api.ZoneAddRequest{Name: name, Provider: provider, Token: token})
}

// servedNames dials the Edge's HTTPS port with the SNI and returns the DNS
// names of the certificate it presents.
func servedNames(t *testing.T, re *RealEdge, sni string) []string {
	t.Helper()
	conn, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(re.Caddy.HTTPSPort)), &tls.Config{ServerName: sni, RootCAs: re.Caddy.Roots})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0].DNSNames
}

// The Zone lab.example.test sits inside the DNS zone example.test, which the
// SOA lookup finds. The wildcard is ordered once, covers the Zone's names, and
// a Route under it is served with it and orders nothing.
func TestZoneAddWildcardObtainsTheWildcardAndARouteUnderItServesWithIt(t *testing.T) {
	svc := NewHTTPService(t, hello())
	fake := StartFakeDNS(t, "example.test", dnsToken)
	pb := StartPebble(t, PebbleOptions{})
	re := StartEdgeWith(t, EdgeOptions{Pebble: pb})
	re.JoinOutpost(t, "nas")

	r, err := zoneAddWildcard(t, re, "lab.example.test", "fake", dnsToken)
	if err != nil {
		t.Fatal(err)
	}
	if r.DNSZone != "example.test" || !slices.Equal(r.Covers, []string{"*.lab.example.test", "lab.example.test"}) || r.Mode != "wildcard" || r.Provider != "fake" {
		t.Fatalf("%+v", r)
	}
	if len(r.Warnings.Warnings) != 0 {
		t.Errorf("warnings: %v", r.Warnings.Warnings)
	}
	// Caddy orders one certificate per name: "*.zone" and "zone".
	orders := pb.Orders()
	if orders != 2 {
		t.Fatalf("%d orders for the wildcard", orders)
	}
	if fake.Presented() == 0 {
		t.Error("the challenge never went through the provider")
	}

	rr := re.AddRouteReply(t, "app.lab.example.test", "http://"+svc.Addr(), "nas")
	if len(rr.Warnings.Warnings) != 0 {
		t.Errorf("route warnings: %v", rr.Warnings.Warnings)
	}
	for _, proto := range Protos {
		if resp, body := get(t, re.Caddy, proto, "app.lab.example.test", "/"); resp.StatusCode != 200 || body != "hello" {
			t.Fatalf("%s: %d %q", proto, resp.StatusCode, body)
		}
	}
	if names := servedNames(t, re, "app.lab.example.test"); !slices.Contains(names, "*.lab.example.test") {
		t.Errorf("the Route is served with %v, not the wildcard", names)
	}
	if n := pb.Orders(); n != orders {
		t.Errorf("the Route under the wildcard ordered a certificate: %d orders", n-orders)
	}
	// zone ls shows the provider and never the token.
	zl, err := api.Call[api.ZoneList](ctx(t), re.API, http.MethodGet, api.PathZones, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(zl.Zones) != 1 || zl.Zones[0].Provider != "fake" {
		t.Errorf("zone ls: %+v", zl)
	}
}

// A token that cannot read the DNS zone is refused and nothing is saved.
func TestZoneAddWildcardRefusesATokenThatCannotReadTheDNSZone(t *testing.T) {
	StartFakeDNS(t, "example.test", dnsToken)
	pb := StartPebble(t, PebbleOptions{})
	re := StartEdgeWith(t, EdgeOptions{Pebble: pb})

	_, err := zoneAddWildcard(t, re, "example.test", "fake", "wr0ng-T0KEN-value")
	var e *api.Error
	if !errors.As(err, &e) || e.Status != http.StatusBadRequest {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Error(), "wr0ng-T0KEN-value") {
		t.Errorf("the refusal carries the token: %v", err)
	}
	if !strings.Contains(err.Error(), "example.test") {
		t.Errorf("the refusal does not name the DNS zone: %v", err)
	}
	if len(re.Store.Snapshot().Zones) != 0 {
		t.Error("the refused Zone was saved")
	}
	if pb.Orders() != 0 {
		t.Error("a certificate was ordered")
	}
}

func TestZoneAddWildcardRefusesAnUnknownProviderAndAMissingToken(t *testing.T) {
	StartFakeDNS(t, "example.test", dnsToken)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	if _, err := zoneAddWildcard(t, re, "example.test", "nope", dnsToken); err == nil || !strings.Contains(err.Error(), "not compiled in") {
		t.Errorf("unknown provider: %v", err)
	}
	if _, err := zoneAddWildcard(t, re, "example.test", "fake", ""); err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("no token: %v", err)
	}
	if len(re.Store.Snapshot().Zones) != 0 {
		t.Error("a refused Zone was saved")
	}
}

// The token is in state.json and in no log line, no error and none of Caddy's
// config (ADR 0005), including when the challenge fails with an error that
// echoes it, as a provider's API error may.
func TestTheTokenIsInNoCaddyConfigNoLogAndNoError(t *testing.T) {
	fake := StartFakeDNS(t, "example.test", dnsToken)
	fake.FailChallenges()
	logs := new(lockedBuf)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), LogOut: logs, LogLevel: slog.LevelDebug, CertWait: 3 * time.Second})

	r, err := zoneAddWildcard(t, re, "example.test", "fake", dnsToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings.Warnings) == 0 {
		t.Fatal("a failing challenge did not warn")
	}
	_, refused := zoneAddWildcard(t, re, "other.example.test", "fake", "wr0ng-token")
	out := fmt.Sprint(r, refused)

	if z := re.Store.Snapshot().Zones; len(z) != 1 || z[0].Token != dnsToken {
		t.Fatalf("the state does not hold the token: %+v", z)
	}
	if cfg := string(caddyx.LoadedConfig()); cfg == "" || strings.Contains(cfg, dnsToken) {
		t.Errorf("Caddy's config: %q", cfg)
	}
	if !strings.Contains(logs.String(), "[redacted]") {
		t.Errorf("the failing challenge never reached the logs redacted:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), dnsToken) {
		t.Errorf("a log line carries the token:\n%s", logs.String())
	}
	if strings.Contains(out, dnsToken) || strings.Contains(out, "wr0ng-token") {
		t.Errorf("an error or warning carries a token: %s", out)
	}
}

// A Hostname two labels below the Zone is not covered by "*.zone": it gets its
// own certificate, and the reply says its name will appear in Certificate
// Transparency logs.
func TestRouteAddBelowTheWildcardGetsItsOwnCertificateAndTheCTNote(t *testing.T) {
	svc := NewHTTPService(t, hello())
	StartFakeDNS(t, "example.test", dnsToken)
	pb := StartPebble(t, PebbleOptions{})
	re := StartEdgeWith(t, EdgeOptions{Pebble: pb})
	re.JoinOutpost(t, "nas")
	if _, err := zoneAddWildcard(t, re, "example.test", "fake", dnsToken); err != nil {
		t.Fatal(err)
	}
	orders := pb.Orders()

	covered := re.AddRouteReply(t, "app.example.test", "http://"+svc.Addr(), "nas")
	if len(covered.Warnings.Warnings) != 0 || pb.Orders() != orders {
		t.Errorf("a covered name: warnings %v, %d new orders", covered.Warnings.Warnings, pb.Orders()-orders)
	}

	deep := re.AddRouteReply(t, "a.b.example.test", "http://"+svc.Addr(), "nas")
	var note bool
	for _, w := range deep.Warnings.Warnings {
		note = note || strings.Contains(w, "Certificate Transparency")
	}
	if !note {
		t.Errorf("no Certificate Transparency note: %v", deep.Warnings.Warnings)
	}
	if pb.Orders() != orders+1 {
		t.Errorf("the deeper name made %d orders", pb.Orders()-orders)
	}
	if names := servedNames(t, re, "a.b.example.test"); !slices.Contains(names, "a.b.example.test") {
		t.Errorf("served with %v", names)
	}
	if resp, body := get(t, re.Caddy, H2, "a.b.example.test", "/"); resp.StatusCode != 200 || body != "hello" {
		t.Errorf("%d %q", resp.StatusCode, body)
	}
}

// Adding and removing a wildcard Zone reload Caddy, and an SSE stream on
// another Route survives both, on every protocol.
func TestWildcardZoneAddAndRmReloadWithoutCuttingAnSSEStream(t *testing.T) {
	stream := NewHTTPService(t, http.HandlerFunc(sseHandler))
	StartFakeDNS(t, "example.test", dnsToken)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "other.test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "s.other.test", "http://"+stream.Addr(), "nas")

	open := func(proto Proto) func() bool {
		req, _ := http.NewRequestWithContext(ctx(t), "GET", "https://s.other.test/sse", nil)
		resp, err := re.Caddy.Client(t, proto).Do(req)
		if err != nil {
			t.Fatalf("%s: %v", proto, err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		br := bufio.NewReader(resp.Body)
		return func() bool { line, err := br.ReadString('\n'); return err == nil && line != "" }
	}
	nexts := map[Proto]func() bool{}
	flowing := func(when string) {
		t.Helper()
		for _, p := range Protos {
			for range 5 {
				if !nexts[p]() {
					t.Fatalf("%s: the stream ended %s", p, when)
				}
			}
		}
	}
	for _, p := range Protos {
		nexts[p] = open(p)
	}
	flowing("before")

	loads := caddyx.Loads()
	if _, err := zoneAddWildcard(t, re, "example.test", "fake", dnsToken); err != nil {
		t.Fatal(err)
	}
	if caddyx.Loads() <= loads {
		t.Error("adding a wildcard Zone did not reload Caddy")
	}
	flowing("after zone add")

	loads = caddyx.Loads()
	if _, err := zoneRm(t, re, "example.test", false); err != nil {
		t.Fatal(err)
	}
	if caddyx.Loads() <= loads {
		t.Error("removing a wildcard Zone did not reload Caddy")
	}
	flowing("after zone rm")
}

// Removing the Zone deletes its token, and a later challenge finds none.
func TestZoneRmDeletesTheToken(t *testing.T) {
	StartFakeDNS(t, "example.test", dnsToken)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	if _, err := zoneAddWildcard(t, re, "example.test", "fake", dnsToken); err != nil {
		t.Fatal(err)
	}
	if _, err := zoneRm(t, re, "example.test", false); err != nil {
		t.Fatal(err)
	}
	if z := re.Store.Snapshot().Zones; len(z) != 0 {
		t.Errorf("%+v", z)
	}
}
