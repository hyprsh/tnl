// Package routes is the seam between whatever owns the Routes (the state store)
// and the Caddy side that serves them. Routes are tnl's table and never Caddy's
// config (ADR 0002): the router reads a Table on every request, and a change is
// one atomic swap, never a reload.
package routes

import (
	"github.com/hyprsh/tnl/internal/dnsname"
	"net/netip"
	"sync"
	"sync/atomic"
)

// Route sends a Hostname to a Service through an Outpost.
type Route struct {
	Hostname string
	Outpost  string // the Outpost's name
	Service  string // host:port; http only for now
}

// Table is what the router reads. Lookup must not block.
type Table interface {
	Lookup(hostname string) (Route, bool)
}

// Outposts says where an Outpost's data port is in the Tunnel.
type Outposts interface {
	Addr(name string) (netip.AddrPort, bool)
}

// Normalize makes a Hostname comparable: lower case, no trailing dot.
func Normalize(h string) string { return dnsname.Normalize(h) }

// Mem is an in-memory Table. The zero value is an empty table ready to use.
type Mem struct {
	m atomic.Pointer[map[string]Route]
}

// Swap replaces every Route at once.
func (t *Mem) Swap(rs []Route) {
	m := make(map[string]Route, len(rs))
	for _, r := range rs {
		m[Normalize(r.Hostname)] = r
	}
	t.m.Store(&m)
}

// Lookup implements Table.
func (t *Mem) Lookup(hostname string) (Route, bool) {
	m := t.m.Load()
	if m == nil {
		return Route{}, false
	}
	r, ok := (*m)[Normalize(hostname)]
	return r, ok
}

// MemOutposts is an in-memory Outposts. The zero value is ready to use.
type MemOutposts struct {
	mu sync.RWMutex
	m  map[string]netip.AddrPort
}

// Set records where the named Outpost's data port is.
func (o *MemOutposts) Set(name string, a netip.AddrPort) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m == nil {
		o.m = map[string]netip.AddrPort{}
	}
	o.m[name] = a
}

// Addr implements Outposts.
func (o *MemOutposts) Addr(name string) (netip.AddrPort, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	a, ok := o.m[name]
	return a, ok
}
