package caddyx

import (
	"context"
	"fmt"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/certmagic"
	"github.com/libdns/libdns"

	"github.com/hyprsh/tnl/internal/edge/dns"
)

func init() { caddy.RegisterModule(DNSProvider{}) }

// DNSProvider is the dns.providers.tnl module: the DNS-01 challenge's way to
// put a TXT record in a wildcard Zone's DNS zone. Its JSON names the Edge and
// the Zone and nothing more. Each call reads the Zone's provider and current
// token from the Runtime and delegates to that provider, so the token is never
// config (ADR 0005) and every error leaves with the token redacted.
type DNSProvider struct {
	Edge string `json:"edge"`
	Zone string `json:"zone"`

	rt *Runtime
}

// CaddyModule implements caddy.Module.
func (DNSProvider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "dns.providers.tnl", New: func() caddy.Module { return new(DNSProvider) }}
}

// Provision implements caddy.Provisioner.
func (p *DNSProvider) Provision(caddy.Context) (err error) {
	p.rt, err = lookupRuntime(p.Edge)
	return err
}

func (p *DNSProvider) client() (dns.Client, error) {
	if p.rt.Zones == nil {
		return nil, fmt.Errorf("no DNS credentials for Zone %s", p.Zone)
	}
	provider, token, ok := p.rt.Zones(p.Zone)
	if !ok {
		return nil, fmt.Errorf("the Zone %s has no DNS credentials: it was removed or is not a wildcard Zone", p.Zone)
	}
	return dns.Open(provider, token)
}

// AppendRecords implements certmagic.DNSProvider.
func (p *DNSProvider) AppendRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	return c.AppendRecords(ctx, zone, recs)
}

// DeleteRecords implements certmagic.DNSProvider.
func (p *DNSProvider) DeleteRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	return c.DeleteRecords(ctx, zone, recs)
}

var (
	_ caddy.Provisioner     = (*DNSProvider)(nil)
	_ certmagic.DNSProvider = (*DNSProvider)(nil)
)
