package allow

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
)

func lookup(m map[string][]string) Lookup {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		ips, ok := m[host]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		var out []netip.Addr
		for _, s := range ips {
			out = append(out, netip.MustParseAddr(s))
		}
		return out, nil
	}
}

func TestEmptyListAllowsEverything(t *testing.T) {
	l, err := Parse("")
	if err != nil || l != nil {
		t.Fatalf("got %v, %v", l, err)
	}
	addr, err := l.Check(context.Background(), "anything:1", nil)
	if err != nil || addr != "anything:1" {
		t.Fatalf("got %q, %v", addr, err)
	}
}

func TestParseRefusesMalformedEntries(t *testing.T) {
	for _, s := range []string{"192.168.1.0/33", "localhost", "localhost:0", "localhost:99999", "a b:80", ":80", "192.168.1.1", "x:80,", ",", "bad_host:80"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("%q was accepted", s)
		}
	}
}

func TestCheck(t *testing.T) {
	l, err := Parse(" 192.168.1.0/24 , localhost:3000 ,[::1]:8080")
	if err != nil {
		t.Fatal(err)
	}
	dns := lookup(map[string][]string{
		"nas.lan":    {"192.168.1.9"},
		"multi.lan":  {"10.0.0.1", "192.168.1.20"},
		"public.lan": {"8.8.8.8"},
		"localhost":  {"127.0.0.1"},
	})
	for _, c := range []struct {
		service string
		allowed bool
		dial    string
	}{
		{"192.168.1.5:80", true, "192.168.1.5:80"}, // literal IP in the CIDR, any port
		{"192.168.1.5:9999", true, "192.168.1.5:9999"},
		{"192.168.2.5:80", false, ""},
		{"nas.lan:443", true, "192.168.1.9:443"},  // resolved address in the CIDR
		{"multi.lan:80", true, "192.168.1.20:80"}, // the matching address is the one dialed
		{"public.lan:80", false, ""},
		{"localhost:3000", true, "localhost:3000"}, // host:port as the Edge sent it
		{"localhost:3001", false, ""},              // 127.0.0.1 is in no CIDR
		{"[::1]:8080", true, "[::1]:8080"},
	} {
		got, err := l.Check(context.Background(), c.service, dns)
		if c.allowed && (err != nil || got != c.dial) {
			t.Errorf("%s: got %q, %v; want %q", c.service, got, err, c.dial)
		}
		if !c.allowed && !errors.Is(err, ErrRefused) {
			t.Errorf("%s: got %q, %v; want ErrRefused", c.service, got, err)
		}
	}
}

func TestCheckPassesOnAResolutionFailure(t *testing.T) {
	l, _ := Parse("10.0.0.0/8")
	_, err := l.Check(context.Background(), "gone.lan:80", lookup(nil))
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("got %v", err)
	}
}

func TestStringListsTheEntries(t *testing.T) {
	l, _ := Parse("192.168.1.0/24,localhost:3000")
	if got := l.String(); got != "192.168.1.0/24,localhost:3000" {
		t.Fatal(got)
	}
	var none *List
	if !strings.Contains(none.String(), "none") {
		t.Fatal(none.String())
	}
}
