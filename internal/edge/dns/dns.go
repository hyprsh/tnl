// Package dns is the Edge's DNS providers: the registry of the libdns
// providers it can drive a DNS-01 challenge through, and the token check. It
// links libdns, so only Edge-only code imports it (scripts/check-outpost-deps.sh).
package dns

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
	"github.com/libdns/libdns"
	"go.uber.org/zap"
)

// Client is what a challenge and the token check ask of a provider.
type Client interface {
	libdns.RecordGetter
	libdns.RecordAppender
	libdns.RecordDeleter
}

// Provider is one entry of the registry: how to make a libdns provider, and
// the names of its credential fields. The Edge holds one token per Zone, which
// goes into each field.
type Provider struct {
	Name   string
	Fields []string
	New    func() Client
}

var (
	mu       sync.RWMutex
	registry = map[string]Provider{}
)

// Register adds a provider. newClient makes an empty libdns provider, and
// fields names the credential fields (its JSON keys) the token goes into.
// Adding a provider is an import and one call.
func Register(name string, newClient func() Client, fields ...string) {
	mu.Lock()
	defer mu.Unlock()
	registry[name] = Provider{Name: name, Fields: fields, New: newClient}
}

func init() {
	Register("cloudflare", func() Client { return new(cloudflare.Provider) }, "api_token")
}

// Lookup finds a provider by name.
func Lookup(name string) (Provider, bool) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// Names lists the providers, sorted, for the refusal that names them.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// Open makes the provider's client with the token in its credential fields.
// Every error the client returns has the token redacted.
func Open(provider, token string) (Client, error) {
	p, ok := Lookup(provider)
	if !ok {
		return nil, fmt.Errorf("DNS provider %q is not compiled in", provider)
	}
	creds := make(map[string]string, len(p.Fields))
	for _, f := range p.Fields {
		creds[f] = token
	}
	b, err := json.Marshal(creds)
	if err != nil {
		return nil, err
	}
	c := p.New()
	if err := json.Unmarshal(b, c); err != nil {
		return nil, Redact(fmt.Errorf("DNS provider %q: %w", provider, err), token)
	}
	return redacting{inner: c, token: token}, nil
}

// Check refuses a token that cannot read the DNS zone. It is the one hard gate
// of `zone add --wildcard`.
func Check(ctx context.Context, provider, token, dnsZone string) error {
	c, err := Open(provider, token)
	if err != nil {
		return err
	}
	// libdns names zones fully qualified.
	if _, err := c.GetRecords(ctx, strings.TrimSuffix(dnsZone, ".")+"."); err != nil {
		return fmt.Errorf("the %s token cannot read the DNS zone %s: %w", provider, dnsZone, err)
	}
	return nil
}

// FindZone finds the provider's DNS zone for a name by walking up from it
// asking for the SOA record, through the resolvers ("host:port"), or the
// system's if there are none. It returns the zone without a trailing dot.
func FindZone(ctx context.Context, name string, resolvers []string) (string, error) {
	z, err := certmagic.FindZoneByFQDN(ctx, zap.NewNop(), name, certmagic.RecursiveNameservers(resolvers))
	if err != nil {
		return "", fmt.Errorf("could not find the DNS zone of %s: %w", name, err)
	}
	return strings.TrimSuffix(z, "."), nil
}

const redactedMark = "[redacted]"

// Redact returns err with the token replaced by [redacted] in its text.
func Redact(err error, token string) error {
	if err == nil || token == "" || !strings.Contains(err.Error(), token) {
		return err
	}
	return &redactedError{msg: strings.ReplaceAll(err.Error(), token, redactedMark), err: err}
}

type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }

// Unwrap lets errors.Is see a cancelled context. Callers print Error, never
// the wrapped error.
func (e *redactedError) Unwrap() error { return e.err }

// redacting wraps a client so no error it returns carries the token.
type redacting struct {
	inner Client
	token string
}

func (r redacting) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	recs, err := r.inner.GetRecords(ctx, zone)
	return recs, Redact(err, r.token)
}

func (r redacting) AppendRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	out, err := r.inner.AppendRecords(ctx, zone, recs)
	return out, Redact(err, r.token)
}

func (r redacting) DeleteRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	out, err := r.inner.DeleteRecords(ctx, zone, recs)
	return out, Redact(err, r.token)
}
