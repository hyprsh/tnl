// Package caddyx embeds Caddy in the Edge: it builds the Caddy config, loads it,
// and holds tnl's Caddy modules (the router and the transport). It is the only
// place that imports Caddy besides the Edge-only files that start it, so the
// Outpost-only build stays Caddy-free.
package caddyx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
	"github.com/caddyserver/certmagic"

	"github.com/hyprsh/tnl/internal/dnsname"
)

// Timeouts are Caddy's server timeouts. Read and write timeouts are always 0.
type Timeouts struct {
	ReadHeader time.Duration
	Idle       time.Duration
	ReadIdle   time.Duration
	WriteIdle  time.Duration
}

// DefaultTimeouts are the values of the spec's section 6.
func DefaultTimeouts() Timeouts {
	return Timeouts{
		ReadHeader: time.Minute,
		Idle:       5 * time.Minute,
		ReadIdle:   5 * time.Minute,
		WriteIdle:  time.Minute,
	}
}

// ACME is the ACME CA the Edge gets its certificates from.
type ACME struct {
	// CA is the directory URL; empty is Let's Encrypt production.
	CA string
	// Email is the account contact; optional.
	Email string
	// TrustedRoots are PEM files of roots to trust for the CA's own TLS, for a
	// CA that isn't publicly trusted, such as Pebble in the harness.
	TrustedRoots []string
}

// Config is what the Caddy config is built from.
type Config struct {
	// EdgeID names the Runtime registered with Register.
	EdgeID string
	// StateDir is the Edge's state directory. Caddy's storage and data directory
	// are StateDir/caddy.
	StateDir string
	// HTTPListen and HTTPSListen are listen addresses, such as ":80" and ":443".
	// The HTTPS port is used for UDP too, for HTTP/3.
	HTTPListen, HTTPSListen string
	// HTTPPort and HTTPSPort are the ports Visitors reach. They default to 80 and
	// 443; a harness sets them to the ports it listens on.
	HTTPPort, HTTPSPort int
	// ACME turns on certificates from an ACME CA, issued on demand for Hostnames
	// that have a Route. Nil keeps Caddy's internal CA, which tests use.
	ACME *ACME
	// Timeouts defaults to DefaultTimeouts.
	Timeouts *Timeouts
	// NoHTTP3 serves HTTP/1.1 and HTTP/2 only, leaving the HTTPS port's UDP side
	// free for something else (WireGuard).
	NoHTTP3 bool
	// RenewCheckInterval is how often Caddy scans for certificates to renew;
	// zero keeps Caddy's 10 min. The Caddy suite shortens it.
	RenewCheckInterval time.Duration
	// Wildcards names the wildcard Zones: each gets a TLS automation policy for
	// "*.zone" and "zone" whose DNS-01 challenge goes through the tnl DNS
	// provider module. The config names the Zone, never a token (ADR 0005).
	Wildcards []string
	// Hostnames are the Route Hostnames the certificate manager hands to Caddy
	// (Start fills them in): the config gives each a policy of its own that
	// isn't on-demand and automates it, so a certificate is obtained for it even
	// where a retired wildcard is also managed, which Caddy would otherwise take
	// as covering it.
	Hostnames []string
	// DNSPropagationTimeout is how long a challenge waits for its TXT record to
	// appear; zero keeps Caddy's 2 min and -1 skips the check, which the harness
	// does because its records are not in DNS.
	DNSPropagationTimeout time.Duration
	// DNSResolvers are the DNS servers the challenges and their SOA lookups ask;
	// none means the system's.
	DNSResolvers []string
}

// retiredNames are the names of the wildcard certificates in storage that
// Caddy no longer manages, because their Zone is not a wildcard now (it was
// switched to per-Hostname, or removed), and that have not expired. The config
// loads them, as certificates nobody renews, so a dropped wildcard keeps
// serving until it expires. Storage is the record, so a restart loses nothing.
func (c Config) retiredNames() (out []any) {
	managed := map[string]bool{}
	for _, z := range c.Wildcards {
		managed[z] = true
	}
	st := &certmagic.FileStorage{Path: filepath.Join(c.StateDir, "caddy")}
	now := time.Now()
	for _, zone := range storedWildcardZones(st) {
		if managed[zone] {
			continue
		}
		// Each name has a certificate of its own, with its own expiry.
		for _, n := range dnsname.WildcardNames(zone) {
			if storedNotAfter(st, n).After(now) {
				out = append(out, n)
			}
		}
	}
	return out
}

// wildcardPolicy is the TLS automation policy of a wildcard Zone. It is not
// on-demand, so certmagic renews its certificate in the background.
func (c Config) wildcardPolicy(zone string) map[string]any {
	iss := c.issuer()
	if c.ACME != nil {
		dns := map[string]any{"provider": map[string]any{"name": "tnl", "edge": c.EdgeID, "zone": zone}}
		if len(c.DNSResolvers) > 0 {
			dns["resolvers"] = c.DNSResolvers
		}
		if c.DNSPropagationTimeout != 0 {
			dns["propagation_timeout"] = int64(c.DNSPropagationTimeout)
		}
		iss["challenges"] = map[string]any{"dns": dns}
	}
	return map[string]any{"subjects": dnsname.WildcardNames(zone), "issuers": []any{iss}}
}

// issuer is the issuer module every automation policy uses.
func (c Config) issuer() map[string]any {
	a := c.ACME
	if a == nil {
		return map[string]any{"module": "internal"}
	}
	iss := map[string]any{"module": "acme", "ca": a.CA}
	if a.CA == "" {
		iss["ca"] = certmagic.LetsEncryptProductionCA
	}
	if a.Email != "" {
		iss["email"] = a.Email
	}
	if len(a.TrustedRoots) > 0 {
		iss["trusted_roots_pem_files"] = a.TrustedRoots
	}
	return iss
}

func (c Config) withDefaults() Config {
	if c.HTTPListen == "" {
		c.HTTPListen = ":80"
	}
	if c.HTTPSListen == "" {
		c.HTTPSListen = ":443"
	}
	if c.HTTPPort == 0 {
		c.HTTPPort = 80
	}
	if c.HTTPSPort == 0 {
		c.HTTPSPort = 443
	}
	if c.Timeouts == nil {
		d := DefaultTimeouts()
		c.Timeouts = &d
	}
	return c
}

// JSON builds the Caddy config. It holds no Routes: those are tnl's table and
// are never Caddy config (ADR 0002).
func (c Config) JSON() ([]byte, error) {
	if c.EdgeID == "" || c.StateDir == "" {
		return nil, errors.New("caddyx: Config needs EdgeID and StateDir")
	}
	c = c.withDefaults()
	to := c.Timeouts

	location := "https://{http.request.host}"
	if c.HTTPSPort != 443 {
		location += fmt.Sprintf(":%d", c.HTTPSPort)
	}
	location += "{http.request.uri}"

	// The wildcard Zones' policies come first, then the catch-all, which is the
	// on-demand fallback. Route Hostnames the certificate manager hands over get
	// a policy of their own that isn't on-demand, because certmagic's
	// maintenance loop skips on-demand certificates.
	var policies, automate []any
	if len(c.Hostnames) > 0 {
		policies = append(policies, map[string]any{"subjects": c.Hostnames, "issuers": []any{c.issuer()}})
		for _, n := range c.Hostnames {
			automate = append(automate, n)
		}
	}
	for _, z := range c.Wildcards {
		policies = append(policies, c.wildcardPolicy(z))
		for _, n := range dnsname.WildcardNames(z) {
			automate = append(automate, n)
		}
	}
	policies = append(policies, map[string]any{"on_demand": true, "issuers": []any{c.issuer()}})
	automation := map[string]any{"policies": policies}
	if c.ACME != nil {
		// Issuance on demand only for Hostnames that have a Route.
		automation["on_demand"] = map[string]any{"permission": map[string]any{"module": "tnl", "edge": c.EdgeID}}
	}
	if c.RenewCheckInterval > 0 {
		automation["renew_interval"] = c.RenewCheckInterval.String()
	}

	protocols := []string{"h1", "h2", "h3"}
	if c.NoHTTP3 {
		protocols = []string{"h1", "h2"}
	}

	tlsApp := map[string]any{"automation": automation}
	certificates := map[string]any{}
	if len(automate) > 0 {
		// A policy's subjects don't start issuance; automating the names does.
		certificates["automate"] = automate
	}
	// Automated under the catch-all, which is on-demand: Caddy loads the
	// certificate from storage and keeps it in its cache across the reload, and
	// nothing renews it, because certmagic's maintenance skips on-demand
	// certificates.
	if retired := c.retiredNames(); len(retired) > 0 {
		certificates["automate"] = append(automate, retired...)
	}
	if len(certificates) > 0 {
		tlsApp["certificates"] = certificates
	}

	cfg := map[string]any{
		"admin":   map[string]any{"disabled": true, "config": map[string]any{"persist": false}},
		"storage": map[string]any{"module": "file_system", "root": filepath.Join(c.StateDir, "caddy")},
		// Caddy's entries leave through tnl's log core, which sets the levels; the
		// log's own writer discards. No "sink": Go's log package follows slog.
		"logging": map[string]any{"logs": map[string]any{"default": map[string]any{
			"level": "DEBUG", "writer": map[string]any{"output": "discard"},
			"core": map[string]any{"module": "tnl", "edge": c.EdgeID},
		}}},
		"apps": map[string]any{
			"http": map[string]any{
				// Zero, so a reload never cuts a stream still running.
				"grace_period": "0s",
				"http_port":    c.HTTPPort,
				"https_port":   c.HTTPSPort,
				"servers": map[string]any{
					"https": map[string]any{
						"listen":    []string{c.HTTPSListen},
						"protocols": protocols,
						// read_timeout and write_timeout stay unset, which is 0.
						"read_header_timeout": to.ReadHeader.String(),
						"idle_timeout":        to.Idle.String(),
						"read_idle_timeout":   to.ReadIdle.String(),
						"write_idle_timeout":  to.WriteIdle.String(),
						"routes": []any{map[string]any{"handle": []any{
							map[string]any{"handler": "tnl_router", "edge": c.EdgeID},
							map[string]any{
								"handler":   "reverse_proxy",
								"transport": map[string]any{"protocol": "tnl", "edge": c.EdgeID},
								// A placeholder: the transport picks the real target per Route.
								"upstreams": []any{map[string]any{"dial": "tnl.invalid:80"}},
							},
						}}},
					},
					// Caddy's automatic redirect only covers hostnames from host
					// matchers, and the router has none, so this server is explicit.
					// It still answers ACME HTTP-01 challenges before its routes run.
					"redirect": map[string]any{
						"listen":          []string{c.HTTPListen},
						"automatic_https": map[string]any{"disable": true},
						"routes": []any{map[string]any{"handle": []any{map[string]any{
							"handler": "static_response", "status_code": 308,
							"headers": map[string]any{"Location": []string{location}},
						}}}},
					},
				},
			},
			"pki": map[string]any{"certificate_authorities": map[string]any{
				"local": map[string]any{"install_trust": false},
			}},
			"tls": tlsApp,
			// The certificate manager records both events and returns.
			"events": map[string]any{"subscriptions": []any{map[string]any{
				"events":   []string{"cert_obtained", "cert_failed"},
				"handlers": []any{map[string]any{"handler": "tnl", "edge": c.EdgeID}},
			}}},
		},
	}
	return json.Marshal(cfg)
}

var (
	loads    atomic.Int64
	lastLoad atomic.Pointer[[]byte]
)

// LoadedConfig is the JSON of the config Start last loaded, for tests that
// show what Caddy was given (the token is not in it).
func LoadedConfig() []byte {
	if p := lastLoad.Load(); p != nil {
		return *p
	}
	return nil
}

// Loads counts how many times Start has loaded a config in this process. Tests
// use it to show that a new Route reloads nothing.
func Loads() int64 { return loads.Load() }

// Start loads the config. A second Start is a reload: it restarts Caddy's apps
// with fresh module instances, and Runtime state carries over. Caddy is a
// process singleton, so only one Edge runs per process.
func Start(c Config) error {
	// Caddy's data directory is $XDG_DATA_HOME/caddy.
	dataHome := c.StateDir
	if err := os.MkdirAll(dataHome, 0o700); err != nil {
		return err
	}
	if err := os.Setenv("XDG_DATA_HOME", dataHome); err != nil {
		return err
	}
	caddy.DefaultStorage = &certmagic.FileStorage{Path: caddy.AppDataDir()}
	raw, err := json.Marshal(c.withDefaults().issuer())
	if err != nil {
		return err
	}
	rt, err := lookupRuntime(c.EdgeID)
	if err != nil {
		return err
	}
	certs := rt.Certs()
	certs.setIssuer(raw)
	certs.SetStateDir(c.StateDir)
	c.Hostnames = certs.configure()
	b, err := c.JSON()
	if err != nil {
		return err
	}
	loads.Add(1)
	if err := caddy.Load(b, true); err != nil {
		return err
	}
	lastLoad.Store(&b)
	certs.start()
	// The new TLS app knows none of the Hostnames handed over before.
	return certs.loaded()
}

// Stop stops Caddy.
func Stop() error { return caddy.Stop() }

// RootCertPath is where Caddy's internal CA keeps its root certificate.
func RootCertPath(stateDir string) string {
	return filepath.Join(stateDir, "caddy", "pki", "authorities", "local", "root.crt")
}
