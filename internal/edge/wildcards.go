package edge

import (
	"context"
	"fmt"
	"strings"

	"github.com/hyprsh/tnl/internal/edge/dns"
	"github.com/hyprsh/tnl/internal/state"
)

// wildcards is the DNS and Caddy side of wildcard Zones, as the API server
// sees it (server.Wildcards). The Caddy parts exist only once Caddy's side is
// built, after the server is; without Caddy they do nothing.
type wildcards struct {
	// certsRef answers Wait, WaitOwn and Settle.
	*certsRef
	// resolvers are the DNS servers the SOA lookup asks; none means the system's.
	resolvers []string
	reload    late[func() error]
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
	return w.reload.get(func() error { return nil })()
}

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
