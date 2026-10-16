package edge

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/hyprsh/tnl/internal/edge/dns"
	"github.com/hyprsh/tnl/internal/state"
)

// wildcards is the DNS and Caddy side of wildcard Zones, as the API server
// sees it (server.Wildcards). The Caddy parts exist only once Caddy's side is
// built, after the server is; without Caddy they do nothing.
type wildcards struct {
	// resolvers are the DNS servers the SOA lookup asks; none means the system's.
	resolvers []string
	certs     *certsRef
	reload    atomic.Pointer[func() error]

	mu      sync.Mutex
	retired map[string]int
}

// Retire implements server.Wildcards.
func (w *wildcards) Retire(names ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.retired == nil {
		w.retired = map[string]int{}
	}
	for _, n := range names {
		w.retired[n]++
	}
}

// Release implements server.Wildcards.
func (w *wildcards) Release(names ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, n := range names {
		if w.retired[n]--; w.retired[n] <= 0 {
			delete(w.retired, n)
		}
	}
}

func (w *wildcards) retiredNames() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Sorted(maps.Keys(w.retired))
}

// WaitOwn waits for a certificate that names the Hostname itself.
func (w *wildcards) WaitOwn(ctx context.Context, name string) error {
	return w.certs.WaitOwn(ctx, name)
}

// Check finds the Zone's DNS zone and refuses a token that cannot read it.
func (w *wildcards) Check(ctx context.Context, provider, token, zone string) (string, error) {
	if _, ok := dns.Lookup(provider); !ok {
		return "", fmt.Errorf("DNS provider %q is not compiled in; this build has %s", provider, strings.Join(dns.Names(), ", "))
	}
	dnsZone, err := dns.FindZone(ctx, zone, w.resolvers)
	if err != nil {
		return "", err
	}
	if err := dns.Check(ctx, provider, token, dnsZone); err != nil {
		return dnsZone, err
	}
	return dnsZone, nil
}

// Reload loads Caddy's config for the Zones as they now stand.
func (w *wildcards) Reload() error {
	if f := w.reload.Load(); f != nil {
		return (*f)()
	}
	return nil
}

// Wait waits for a certificate for the name.
func (w *wildcards) Wait(ctx context.Context, name string) error { return w.certs.Wait(ctx, name) }

func wildcardZones(st *state.State) []string { return st.WildcardZones() }

// checkProviders refuses a state with a wildcard Zone whose provider this
// build does not have.
func checkProviders(st *state.State) error {
	for _, z := range st.Zones {
		if z.Mode != state.Wildcard {
			continue
		}
		if _, ok := dns.Lookup(z.Provider); !ok {
			return fmt.Errorf("zone %q: DNS provider %q is not compiled in", z.Name, z.Provider)
		}
	}
	return nil
}
