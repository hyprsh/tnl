// Package allow is the Outpost's allowlist (--allow): which Services the Edge
// may name. It imports nothing from the Edge or Caddy.
package allow

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/hyprsh/tnl/internal/dnsname"
)

// ErrRefused says the allowlist does not list the Service.
var ErrRefused = errors.New("the allowlist does not list the Service")

// Lookup resolves a name to its addresses.
type Lookup func(ctx context.Context, host string) ([]netip.Addr, error)

// List is a parsed allowlist. A nil *List allows everything.
type List struct {
	raw      []string
	prefixes []netip.Prefix
	exact    map[string]bool // host:port, as the Edge sends it (lower case)
}

// Parse reads a comma-separated list of CIDRs and host:port pairs. An empty
// string is no list: everything is allowed.
func Parse(s string) (*List, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	l := &List{exact: map[string]bool{}}
	for _, e := range strings.Split(s, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			return nil, fmt.Errorf("--allow has an empty entry in %q", s)
		}
		l.raw = append(l.raw, e)
		if strings.Contains(e, "/") {
			p, err := netip.ParsePrefix(e)
			if err != nil {
				return nil, fmt.Errorf("--allow entry %q is not a CIDR: %v", e, err)
			}
			l.prefixes = append(l.prefixes, p.Masked())
			continue
		}
		host, port, err := net.SplitHostPort(e)
		if err != nil {
			return nil, fmt.Errorf("--allow entry %q must be a CIDR such as 192.168.1.0/24 or host:port", e)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("--allow entry %q has a bad port: use 1 to 65535", e)
		}
		if _, err := netip.ParseAddr(host); err != nil {
			if err := dnsname.CheckName(strings.ToLower(host)); err != nil {
				return nil, fmt.Errorf("--allow entry %q has a bad host: %v", e, err)
			}
		}
		// The Edge sends Service hosts in lower case.
		l.exact[net.JoinHostPort(strings.ToLower(host), port)] = true
	}
	return l, nil
}

// String is the list as it is logged.
func (l *List) String() string {
	if l == nil {
		return "none"
	}
	return strings.Join(l.raw, ",")
}

// Check decides whether the Service (host:port, as the Edge sent it) may be
// dialed. It returns the address to dial: the Service itself, or, when a CIDR
// let it through, the resolved address that matched, so the address checked is
// the one dialed. A refusal is ErrRefused; a failed lookup is its own error.
func (l *List) Check(ctx context.Context, service string, lookup Lookup) (string, error) {
	if l == nil || l.exact[service] {
		return service, nil
	}
	host, port, err := net.SplitHostPort(service)
	if err != nil || len(l.prefixes) == 0 {
		return "", ErrRefused
	}
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else if ips, err = lookup(ctx, host); err != nil {
		return "", err
	}
	for _, ip := range ips {
		ip = ip.Unmap()
		for _, p := range l.prefixes {
			if p.Contains(ip) {
				return net.JoinHostPort(ip.String(), port), nil
			}
		}
	}
	return "", ErrRefused
}
