package harness

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/libdns/libdns"
	mdns "github.com/miekg/dns"

	"github.com/hyprsh/tnl/internal/edge/dns"
)

// The "fake" DNS provider is compiled into the harness, which no release
// binary imports (spec section 11): a wildcard Zone names it as its provider.
func init() {
	dns.Register("fake", func() dns.Client { return new(fakeProvider) }, "api_token")
}

// FakeDNS is one DNS zone held by the fake provider. Its token is the only one
// that can read or change it; any other is refused with an error that echoes
// the token it was given, as a real provider's API error may, so tests can show
// the Edge redacts it.
type FakeDNS struct {
	Zone  string
	token string

	mu      sync.Mutex
	records []libdns.Record
	// appended counts the records ever presented, deleted those removed.
	appended, deleted int
	failChallenges    bool
}

// FailChallenges makes the provider refuse to present challenge records, with
// an error that echoes the token.
func (f *FakeDNS) FailChallenges() { f.mu.Lock(); defer f.mu.Unlock(); f.failChallenges = true }

var (
	fakeMu    sync.Mutex
	fakeZones = map[string]*FakeDNS{}
)

// StartFakeDNS makes the fake provider serve the DNS zone, readable with token,
// until the test ends. The Edge's SOA lookup finds it too.
func StartFakeDNS(t testing.TB, zone, token string) *FakeDNS {
	t.Helper()
	f := &FakeDNS{Zone: zone, token: token}
	fakeMu.Lock()
	fakeZones[zone] = f
	fakeMu.Unlock()
	t.Cleanup(func() {
		fakeMu.Lock()
		delete(fakeZones, zone)
		fakeMu.Unlock()
	})
	return f
}

// Presented is how many challenge records were put in the zone, and Cleaned how
// many were taken out again.
func (f *FakeDNS) Presented() int { f.mu.Lock(); defer f.mu.Unlock(); return f.appended }

// Cleaned is how many challenge records were deleted.
func (f *FakeDNS) Cleaned() int { f.mu.Lock(); defer f.mu.Unlock(); return f.deleted }

// fakeDNSServer starts, once per test, a DNS server on loopback that answers
// SOA queries for the fake zones and nothing else, and returns its address. The
// Edge's SOA lookup and certmagic's both ask it, so the real lookup code runs.
func fakeDNSServer(t testing.TB) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &mdns.Server{PacketConn: pc, Handler: mdns.HandlerFunc(serveFakeSOA)}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go srv.ActivateAndServe()
	<-started
	t.Cleanup(func() { srv.Shutdown() })
	return pc.LocalAddr().String()
}

func serveFakeSOA(w mdns.ResponseWriter, r *mdns.Msg) {
	m := new(mdns.Msg)
	m.SetReply(r)
	m.Authoritative = true
	q := r.Question[0]
	name := strings.TrimSuffix(strings.ToLower(q.Name), ".")
	fakeMu.Lock()
	_, ok := fakeZones[name]
	fakeMu.Unlock()
	switch {
	case ok && q.Qtype == mdns.TypeSOA:
		m.Answer = append(m.Answer, &mdns.SOA{
			Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeSOA, Class: mdns.ClassINET, Ttl: 60},
			Ns:  "ns." + q.Name, Mbox: "hostmaster." + q.Name, Serial: 1, Refresh: 60, Retry: 60, Expire: 60, Minttl: 60,
		})
	case !ok:
		m.Rcode = mdns.RcodeNameError
	}
	w.WriteMsg(m)
}

type fakeProvider struct {
	Token string `json:"api_token"`
}

func (p *fakeProvider) zone(zone string) (*FakeDNS, error) {
	zone = strings.TrimSuffix(zone, ".")
	fakeMu.Lock()
	f := fakeZones[zone]
	fakeMu.Unlock()
	if f == nil {
		return nil, fmt.Errorf("fake DNS: no zone %s (token %s)", zone, p.Token)
	}
	if p.Token != f.token {
		return nil, fmt.Errorf("fake DNS: 403 forbidden: token %s cannot access zone %s", p.Token, zone)
	}
	return f, nil
}

func (p *fakeProvider) GetRecords(_ context.Context, zone string) ([]libdns.Record, error) {
	f, err := p.zone(zone)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]libdns.Record(nil), f.records...), nil
}

func (p *fakeProvider) AppendRecords(_ context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	f, err := p.zone(zone)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failChallenges {
		return nil, fmt.Errorf("fake DNS: 500 from the API for token %s", p.Token)
	}
	f.records = append(f.records, recs...)
	f.appended += len(recs)
	return recs, nil
}

func (p *fakeProvider) DeleteRecords(_ context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	f, err := p.zone(zone)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted += len(recs)
	return recs, nil
}
