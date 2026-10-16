// Package state is the Edge's state: the schema of state.json, the checks it
// must pass whole, and the store that is its only writer. It imports neither
// Caddy nor the tunnel.
package state

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/statefile"
)

// Version is the schema version this tnl reads and writes.
const Version = 1

// Mode is a Zone's certificate mode.
type Mode string

// Zone modes.
const (
	PerHostname Mode = "per-hostname"
	Wildcard    Mode = "wildcard"
)

// State is one immutable-by-convention snapshot of everything the Edge must
// remember. Never modify a State you got from Store.Snapshot; Update hands its
// function a private copy. Declarations arrive in slice 8.
type State struct {
	Version    int
	PrivateKey keys.Key // the Edge's WireGuard private key
	// NextAddr is the Tunnel-address counter: addresses addr.Outpost(0) up to
	// but not including addr.Outpost(NextAddr) have been handed out and are
	// never reused.
	NextAddr int
	Outposts []Outpost
	Routes   []Route
	Zones    []Zone
}

// Outpost is one joined or pending Outpost.
type Outpost struct {
	Name      string
	PublicKey keys.Key
	Address   netip.Addr
	Endpoint  netip.AddrPort // last WireGuard endpoint; zero until known
	// For an Outpost that hasn't joined, or was rekeyed and hasn't joined again.
	OneTimeKey   *keys.Key
	TokenExpires time.Time
}

// Route sends a Hostname to a Service through an Outpost. Its Zone is worked
// out from the Hostname.
type Route struct {
	Hostname           string
	Outpost            string
	Service            string
	InsecureSkipVerify bool
	HostHeader         string
}

// Zone is a domain the Edge serves Hostnames under, with its certificate mode.
type Zone struct {
	Name     string
	Mode     Mode   // PerHostname or Wildcard
	Provider string // wildcard only
	Token    string // wildcard only
}

// AllocAddr hands out the next Tunnel address and advances the counter.
func (s *State) AllocAddr() netip.Addr {
	a := addr.Outpost(s.NextAddr)
	s.NextAddr++
	return a
}

// RoutesVia returns the Hostnames of the Routes through the named Outpost.
func (s *State) RoutesVia(outpost string) []string {
	var hosts []string
	for _, r := range s.Routes {
		if r.Outpost == outpost {
			hosts = append(hosts, r.Hostname)
		}
	}
	return hosts
}

// RemoveRoutesVia removes the Routes through the named Outpost and returns
// their Hostnames.
func (s *State) RemoveRoutesVia(outpost string) []string {
	return s.RemoveRoutes(func(r Route) bool { return r.Outpost == outpost })
}

// RemoveRoutes removes the Routes for which gone is true and returns their
// Hostnames. Zone and Outpost removal share it, and slice 8 sends a declared
// Route back to waiting from here.
func (s *State) RemoveRoutes(gone func(Route) bool) []string {
	var hosts []string
	kept := make([]Route, 0, len(s.Routes))
	for _, r := range s.Routes {
		if gone(r) {
			hosts = append(hosts, r.Hostname)
			continue
		}
		kept = append(kept, r)
	}
	s.Routes = kept
	return hosts
}

// Clone returns a deep copy.
func (s *State) Clone() *State {
	c := *s
	c.Outposts = make([]Outpost, len(s.Outposts))
	for i, o := range s.Outposts {
		if o.OneTimeKey != nil {
			k := *o.OneTimeKey
			o.OneTimeKey = &k
		}
		c.Outposts[i] = o
	}
	c.Routes = append([]Route(nil), s.Routes...)
	c.Zones = append([]Zone(nil), s.Zones...)
	return &c
}

// InvalidError names the entry that failed a check.
type InvalidError struct {
	Entry  string // for example `route "app.example.com"`
	Reason string
}

func (e *InvalidError) Error() string { return e.Entry + ": " + e.Reason }

func invalid(entry, format string, a ...any) error {
	return &InvalidError{Entry: entry, Reason: fmt.Sprintf(format, a...)}
}

// Validate checks the state whole.
func (s *State) Validate() error {
	if s.Version != Version {
		return invalid("state", "schema version %d, this tnl knows %d", s.Version, Version)
	}
	zones := map[string]bool{}
	for _, z := range s.Zones {
		e := fmt.Sprintf("zone %q", z.Name)
		if z.Name == "" {
			return invalid(e, "no name")
		}
		if zones[z.Name] {
			return invalid(e, "listed twice")
		}
		zones[z.Name] = true
		switch z.Mode {
		case PerHostname:
		case Wildcard:
			if z.Provider == "" || z.Token == "" {
				return invalid(e, "a wildcard Zone needs a DNS provider and a token")
			}
		default:
			return invalid(e, "mode %q is neither %s nor %s", z.Mode, PerHostname, Wildcard)
		}
	}

	if s.NextAddr < 0 {
		return invalid("state", "address counter %d is negative", s.NextAddr)
	}
	names := map[string]bool{}
	addrs := map[netip.Addr]string{}
	for _, o := range s.Outposts {
		e := fmt.Sprintf("outpost %q", o.Name)
		if o.Name == "" {
			return invalid(e, "no name")
		}
		if names[o.Name] {
			return invalid(e, "listed twice")
		}
		names[o.Name] = true
		if prev, dup := addrs[o.Address]; dup {
			return invalid(e, "Tunnel address %s is also outpost %q's", o.Address, prev)
		}
		addrs[o.Address] = o.Name
		if (o.OneTimeKey != nil) != !o.TokenExpires.IsZero() {
			return invalid(e, "a one-time key and a token expiry go together")
		}
		idx, ok := addr.OutpostIndex(o.Address)
		if !ok {
			return invalid(e, "Tunnel address %s is not an Outpost address in %s", o.Address, addr.Range)
		}
		if idx >= s.NextAddr {
			return invalid(e, "Tunnel address %s is not below the counter (%d)", o.Address, s.NextAddr)
		}
	}

	hosts := map[string]bool{}
	for _, r := range s.Routes {
		e := fmt.Sprintf("route %q", r.Hostname)
		if r.Hostname == "" {
			return invalid(e, "no Hostname")
		}
		if hosts[r.Hostname] {
			return invalid(e, "Hostname listed twice")
		}
		hosts[r.Hostname] = true
		if !names[r.Outpost] {
			return invalid(e, "outpost %q does not exist", r.Outpost)
		}
		if strings.Contains(r.Hostname, "*") {
			return invalid(e, "a Route takes one exact Hostname; wildcard Routes are not supported")
		}
		if !s.InZone(r.Hostname) {
			return invalid(e, "Hostname is inside no Zone")
		}
		if _, err := ParseService(r.Service); err != nil {
			return invalid(e, "%v", err)
		}
		if err := CheckHostHeader(r.HostHeader); err != nil {
			return invalid(e, "%v", err)
		}
	}
	return nil
}

// InZone reports whether host is a Zone's own name or lies below one.
func (s *State) InZone(host string) bool {
	_, ok := s.ZoneOf(host)
	return ok
}

// ZoneOf returns the Zone that holds host: the longest one that contains it.
func (s *State) ZoneOf(host string) (Zone, bool) {
	names := make([]string, len(s.Zones))
	for i, z := range s.Zones {
		names[i] = z.Name
	}
	name, ok := dnsname.LongestZone(host, names)
	if !ok {
		return Zone{}, false
	}
	return s.Zone(name)
}

// Zone returns the Zone of that exact name.
func (s *State) Zone(name string) (Zone, bool) {
	for _, z := range s.Zones {
		if z.Name == name {
			return z, true
		}
	}
	return Zone{}, false
}

// WildcardZones returns the names of the wildcard Zones, sorted.
func (s *State) WildcardZones() []string {
	var out []string
	for _, z := range s.Zones {
		if z.Mode == Wildcard {
			out = append(out, z.Name)
		}
	}
	slices.Sort(out)
	return out
}

// RoutesStrandedBy returns the Hostnames of the Routes that only the named Zone
// contains: removing it would leave them outside every Zone.
func (s *State) RoutesStrandedBy(zone string) []string {
	rest := &State{Zones: slices.DeleteFunc(slices.Clone(s.Zones), func(z Zone) bool { return z.Name == zone })}
	var hosts []string
	for _, r := range s.Routes {
		if z, in := s.ZoneOf(r.Hostname); in && z.Name == zone && !rest.InZone(r.Hostname) {
			hosts = append(hosts, r.Hostname)
		}
	}
	return hosts
}

// On-disk form. Keys are text there, and parsed here so a bad one is reported
// with the entry it belongs to.
type diskState struct {
	Version    int           `json:"version"`
	PrivateKey string        `json:"private_key"`
	NextAddr   int           `json:"next_address"`
	Outposts   []diskOutpost `json:"outposts"`
	Routes     []diskRoute   `json:"routes"`
	Zones      []diskZone    `json:"zones"`
}

type diskOutpost struct {
	Name         string     `json:"name"`
	PublicKey    string     `json:"public_key"`
	Address      string     `json:"address"`
	Endpoint     string     `json:"endpoint,omitempty"`
	OneTimeKey   string     `json:"one_time_key,omitempty"`
	TokenExpires *time.Time `json:"token_expires,omitempty"`
}

// diskRoute and diskZone are the on-disk spellings of Route and Zone.
type diskRoute struct {
	Hostname           string `json:"hostname"`
	Outpost            string `json:"outpost"`
	Service            string `json:"service"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
	HostHeader         string `json:"host_header,omitempty"`
}

type diskZone struct {
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	Provider string `json:"provider,omitempty"`
	Token    string `json:"token,omitempty"`
}

func encode(s *State) ([]byte, error) {
	d := diskState{Version: s.Version, NextAddr: s.NextAddr, Outposts: []diskOutpost{}, Routes: []diskRoute{}, Zones: []diskZone{}}
	d.PrivateKey = keyText(s.PrivateKey)
	for _, o := range s.Outposts {
		do := diskOutpost{Name: o.Name, PublicKey: keyText(o.PublicKey), Address: o.Address.String()}
		if o.Endpoint.IsValid() {
			do.Endpoint = o.Endpoint.String()
		}
		if o.OneTimeKey != nil {
			do.OneTimeKey = keyText(*o.OneTimeKey)
		}
		if !o.TokenExpires.IsZero() {
			t := o.TokenExpires
			do.TokenExpires = &t
		}
		d.Outposts = append(d.Outposts, do)
	}
	for _, r := range s.Routes {
		d.Routes = append(d.Routes, diskRoute(r))
	}
	for _, z := range s.Zones {
		d.Zones = append(d.Zones, diskZone{Name: z.Name, Mode: string(z.Mode), Provider: z.Provider, Token: z.Token})
	}
	b, err := json.MarshalIndent(d, "", "  ")
	return append(b, '\n'), err
}

func keyText(k keys.Key) string { b, _ := k.MarshalText(); return string(b) }

func parseKey(entry, field, s string) (keys.Key, error) {
	var k keys.Key
	if err := k.UnmarshalText([]byte(s)); err != nil {
		return k, invalid(entry, "%s: %v", field, err)
	}
	return k, nil
}

// decode reads and checks a whole state.json. It never returns part of one.
func decode(raw []byte) (*State, error) {
	var d diskState
	if err := statefile.Decode(raw, FileName, Version, &d); err != nil {
		return nil, err
	}
	s := &State{Version: d.Version, NextAddr: d.NextAddr}
	var err error
	if s.PrivateKey, err = parseKey("state", "private_key", d.PrivateKey); err != nil {
		return nil, err
	}
	for _, do := range d.Outposts {
		e := fmt.Sprintf("outpost %q", do.Name)
		o := Outpost{Name: do.Name}
		if o.PublicKey, err = parseKey(e, "public_key", do.PublicKey); err != nil {
			return nil, err
		}
		if o.Address, err = netip.ParseAddr(do.Address); err != nil {
			return nil, invalid(e, "address: %v", err)
		}
		if do.Endpoint != "" {
			if o.Endpoint, err = netip.ParseAddrPort(do.Endpoint); err != nil {
				return nil, invalid(e, "endpoint: %v", err)
			}
		}
		if do.OneTimeKey != "" {
			k, err := parseKey(e, "one_time_key", do.OneTimeKey)
			if err != nil {
				return nil, err
			}
			o.OneTimeKey = &k
		}
		if do.TokenExpires != nil {
			o.TokenExpires = *do.TokenExpires
		}
		s.Outposts = append(s.Outposts, o)
	}
	for _, r := range d.Routes {
		s.Routes = append(s.Routes, Route(r))
	}
	for _, z := range d.Zones {
		s.Zones = append(s.Zones, Zone{Name: z.Name, Mode: Mode(z.Mode), Provider: z.Provider, Token: z.Token})
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}
