package zones

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/edge/caddyx"

	. "github.com/hyprsh/tnl/internal/harness"
)

func zoneEdit(t *testing.T, re *RealEdge, req api.ZoneEditRequest) (api.ZoneEditReply, error) {
	t.Helper()
	return api.Call[api.ZoneEditReply](Ctx(t), re.API, http.MethodPost, api.PathZoneEdit, req)
}

// poller is a Visitor that opens a new TLS connection to each Hostname over and
// over, and counts every handshake that fails.
//
// A reset counts as a failure too: a reload drops no connection waiting to be
// accepted.
type poller struct {
	mu     sync.Mutex
	hosts  []string
	fails  []string
	served map[string]map[string]bool // Hostname -> the certificate names it was shown
	n      int
	stop   chan struct{}
	done   chan struct{}
}

func startPoller(t *testing.T, re *RealEdge, hosts ...string) *poller {
	p := &poller{hosts: hosts, served: map[string]map[string]bool{}, stop: make(chan struct{}), done: make(chan struct{})}
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(re.Caddy.HTTPSPort))
	go func() {
		defer close(p.done)
		for {
			for _, h := range hosts {
				d := &net.Dialer{Timeout: 5 * time.Second}
				conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: h, RootCAs: re.Caddy.Roots})
				p.mu.Lock()
				p.n++
				if err != nil {
					p.fails = append(p.fails, h+": "+err.Error())
				} else {
					if p.served[h] == nil {
						p.served[h] = map[string]bool{}
					}
					for _, n := range conn.ConnectionState().PeerCertificates[0].DNSNames {
						p.served[h][n] = true
					}
					conn.Close()
				}
				p.mu.Unlock()
			}
			select {
			case <-p.stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	t.Cleanup(func() { p.finish() })
	return p
}

func (p *poller) finish() (handshakes int, fails []string) {
	select {
	case <-p.stop:
	default:
		close(p.stop)
	}
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n, slices.Clone(p.fails)
}

func (p *poller) sawCert(host, name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.served[host][name]
}

// A Visitor polling two Hostnames through per-Hostname, wildcard and
// per-Hostname again never gets a TLS error, and the certificates change hands
// as the spec says: app's own certificate stops renewing once the wildcard is
// in, and the dropped wildcard serves new, whose Route came while it was a
// wildcard, until new has a certificate of its own.
func TestZoneEditSwitchesModesWithoutATLSError(t *testing.T) {
	svc := NewHTTPService(t, hello())
	StartFakeDNS(t, "example.test", dnsToken)
	pb := StartPebble(t, PebbleOptions{})
	re := StartEdgeWith(t, EdgeOptions{Pebble: pb})
	re.AddZone(t, "example.test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.example.test", "http://"+svc.Addr(), "nas")
	if names := servedNames(t, re, "app.example.test"); !slices.Contains(names, "app.example.test") {
		t.Fatalf("per-Hostname: served with %v", names)
	}
	if re.Certs.Managing() != 1 {
		t.Fatalf("managing %d", re.Certs.Managing())
	}
	v := startPoller(t, re, "app.example.test")

	r, err := zoneEdit(t, re, api.ZoneEditRequest{Name: "example.test", Wildcard: "fake", Token: dnsToken})
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != "wildcard" || r.DNSZone != "example.test" || !slices.Equal(r.Covers, []string{"*.example.test", "example.test"}) || len(r.Warnings.Warnings) != 0 {
		t.Fatalf("%+v", r)
	}
	if re.Certs.Managing() != 0 {
		t.Errorf("%d Hostnames still renew their own certificate after the wildcard came in", re.Certs.Managing())
	}
	if z := re.Store.Snapshot().Zones; len(z) != 1 || z[0].Token != dnsToken || z[0].Provider != "fake" {
		t.Fatalf("the state: %+v", z)
	}
	if resp, body := Get(t, re.Caddy, H2, "app.example.test", "/"); resp.StatusCode != 200 || body != "hello" {
		t.Fatalf("wildcard: %d %q", resp.StatusCode, body)
	}
	// A new Route in the wildcard Zone is served by the wildcard, with no order.
	orders := pb.Orders()
	re.AddRoute(t, "new.example.test", "http://"+svc.Addr(), "nas")
	if pb.Orders() != orders {
		t.Errorf("a Route under the wildcard ordered a certificate")
	}
	if names := servedNames(t, re, "new.example.test"); !slices.Contains(names, "*.example.test") {
		t.Fatalf("new is served with %v", names)
	}
	w := startPoller(t, re, "new.example.test")

	// Back: new has no certificate of its own yet, and the wildcard serves it
	// until it has.
	r, err = zoneEdit(t, re, api.ZoneEditRequest{Name: "example.test", PerHostname: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != "per-hostname" || len(r.Warnings.Warnings) != 0 {
		t.Fatalf("%+v", r)
	}
	for _, h := range []string{"app.example.test", "new.example.test"} {
		if names := servedNames(t, re, h); !slices.Contains(names, h) {
			t.Errorf("after switching back, %s is served with %v", h, names)
		}
	}
	if re.Certs.Managing() != 2 {
		t.Errorf("managing %d after switching back", re.Certs.Managing())
	}
	if resp, body := Get(t, re.Caddy, H1, "new.example.test", "/"); resp.StatusCode != 200 || body != "hello" {
		t.Fatalf("per-Hostname again: %d %q", resp.StatusCode, body)
	}
	data, err := os.ReadFile(re.StateDir + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), dnsToken) || strings.Contains(string(data), "fake") {
		t.Errorf("state.json still holds the token or provider:\n%s", data)
	}
	if z := re.Store.Snapshot().Zones; len(z) != 1 || z[0].Token != "" || z[0].Provider != "" {
		t.Errorf("the state: %+v", z)
	}

	for _, p := range []*poller{v, w} {
		n, fails := p.finish()
		if len(fails) != 0 {
			t.Fatalf("%d of %d handshakes failed, first: %s", len(fails), n, fails[0])
		}
		if n < 20 {
			t.Errorf("only %d handshakes", n)
		}
	}
	if !w.sawCert("new.example.test", "*.example.test") {
		t.Error("the wildcard never served new")
	}
}

// Adding lab.example.test as a wildcard Zone under a per-Hostname example.test
// moves app.lab.example.test under it with no TLS error.
func TestAddingANestedWildcardZoneMovesTheRoutesWithoutATLSError(t *testing.T) {
	svc := NewHTTPService(t, hello())
	StartFakeDNS(t, "example.test", dnsToken)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "example.test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.lab.example.test", "http://"+svc.Addr(), "nas")
	v := startPoller(t, re, "app.lab.example.test")

	if _, err := zoneAddWildcard(t, re, "lab.example.test", "fake", dnsToken); err != nil {
		t.Fatal(err)
	}
	if re.Certs.Managing() != 0 {
		t.Errorf("%d Hostnames still renew their own certificate", re.Certs.Managing())
	}
	if resp, body := Get(t, re.Caddy, H2, "app.lab.example.test", "/"); resp.StatusCode != 200 || body != "hello" {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	time.Sleep(300 * time.Millisecond) // lets the poller keep handshaking past the edit, to catch a late failure
	n, fails := v.finish()
	if len(fails) != 0 {
		t.Fatalf("%d of %d handshakes failed, first: %s", len(fails), n, fails[0])
	}
	// The Route's own certificate serves until it expires; a Route made now is
	// served by the nested wildcard.
	re.AddRoute(t, "new.lab.example.test", "http://"+svc.Addr(), "nas")
	if names := servedNames(t, re, "new.lab.example.test"); !slices.Contains(names, "*.lab.example.test") {
		t.Errorf("new.lab.example.test is served with %v", names)
	}
}

// A bad token is refused and the old one stays. A good one replaces it with no
// reload, so an open WebSocket-like stream stays open.
func TestZoneEditTokenChecksThenReplacesWithoutAReload(t *testing.T) {
	stream := NewHTTPService(t, http.HandlerFunc(SSEHandler))
	fake := StartFakeDNS(t, "example.test", dnsToken)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.JoinOutpost(t, "nas")
	if _, err := zoneAddWildcard(t, re, "example.test", "fake", dnsToken); err != nil {
		t.Fatal(err)
	}
	re.AddRoute(t, "s.example.test", "http://"+stream.Addr(), "nas")
	next := OpenStream(t, re, "s.example.test")
	if !next() {
		t.Fatal("the stream did not start")
	}
	loads := caddyx.Loads()

	_, err := zoneEdit(t, re, api.ZoneEditRequest{Name: "example.test", Token: "wr0ng-T0KEN-value"})
	var e *api.Error
	if !errors.As(err, &e) || e.Status != http.StatusBadRequest || strings.Contains(err.Error(), "wr0ng-T0KEN-value") {
		t.Fatalf("a bad token: %v", err)
	}
	if z := re.Store.Snapshot().Zones; z[0].Token != dnsToken {
		t.Fatalf("the bad token replaced the old one: %+v", z)
	}

	fake.SetToken("n3w-T0KEN-0123456789")
	if _, err := zoneEdit(t, re, api.ZoneEditRequest{Name: "example.test", Token: "n3w-T0KEN-0123456789"}); err != nil {
		t.Fatal(err)
	}
	if z := re.Store.Snapshot().Zones; z[0].Token != "n3w-T0KEN-0123456789" || z[0].Mode != "wildcard" {
		t.Fatalf("the state: %+v", z)
	}
	if caddyx.Loads() != loads {
		t.Error("replacing the token reloaded Caddy")
	}
	for range 5 {
		if !next() {
			t.Fatal("the stream closed")
		}
	}
}

func TestZoneEditRefusesWhatItCannotDo(t *testing.T) {
	StartFakeDNS(t, "example.test", dnsToken)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "plain.example.test")
	for name, req := range map[string]api.ZoneEditRequest{
		"nothing":                {Name: "plain.example.test"},
		"two changes":            {Name: "plain.example.test", Wildcard: "fake", PerHostname: true, Token: "x"},
		"no such Zone":           {Name: "nope.example.test", PerHostname: true},
		"token on per-Hostname":  {Name: "plain.example.test", Token: dnsToken},
		"already per-Hostname":   {Name: "plain.example.test", PerHostname: true},
		"wildcard without token": {Name: "plain.example.test", Wildcard: "fake"},
		"wildcard, unknown":      {Name: "plain.example.test", Wildcard: "nope", Token: "x"},
		"wildcard, a bad token":  {Name: "plain.example.test", Wildcard: "fake", Token: "wr0ng"},
		"per-hostname and token": {Name: "plain.example.test", PerHostname: true, Token: "x"},
	} {
		if _, err := zoneEdit(t, re, req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if z := re.Store.Snapshot().Zones; len(z) != 1 || z[0].Mode != "per-hostname" {
		t.Errorf("a refusal changed the state: %+v", z)
	}
}
