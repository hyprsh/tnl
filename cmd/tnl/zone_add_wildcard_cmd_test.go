//go:build !outpost

package main

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
)

// gotToken is a server.Wildcards that records what the Edge was given.
type gotToken struct {
	mu                    sync.Mutex
	provider, token, zone string
}

func (g *gotToken) Check(_ context.Context, provider, token, zone string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.provider, g.token, g.zone = provider, token, zone
	return "example.com", nil
}
func (g *gotToken) Reload() error                         { return nil }
func (g *gotToken) Wait(context.Context, string) error    { return nil }
func (g *gotToken) WaitOwn(context.Context, string) error { return nil }
func (g *gotToken) Retire(...string)                      {}
func (g *gotToken) Release(...string)                     {}

func withStdin(t *testing.T, s string) {
	t.Helper()
	old := stdin
	stdin = strings.NewReader(s)
	t.Cleanup(func() { stdin = old })
}

func TestZoneAddWildcardReadsTheTokenFromStdinAndPrintsTheDNSZoneAndNames(t *testing.T) {
	g := new(gotToken)
	edgeWildcards = g
	t.Cleanup(func() { edgeWildcards = nil })
	runningEdge(t)
	// A variable is never a way to pass the token.
	t.Setenv("TNL_DNS_TOKEN", "from-the-environment")
	t.Setenv("TNL_TOKEN", "from-the-environment")
	withStdin(t, "from-stdin\n")

	out, errOut, code, err := tnlBoth("zone", "add", "app.example.com", "--wildcard", "cloudflare")
	if err != nil || code != 0 {
		t.Fatalf("%q %q %v", out, errOut, err)
	}
	if g.token != "from-stdin" || g.provider != "cloudflare" || g.zone != "app.example.com" {
		t.Errorf("the Edge got %q %q %q", g.provider, g.token, g.zone)
	}
	for _, want := range []string{"DNS zone example.com", "added Zone app.example.com", "*.app.example.com, app.example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q: %q", want, out)
		}
	}
	if strings.Contains(out+errOut, "from-stdin") {
		t.Errorf("the token was printed: %q %q", out, errOut)
	}
	ls, _, err := tnl("zone", "ls")
	if err != nil || !strings.Contains(ls, "wildcard") || !strings.Contains(ls, "cloudflare") || strings.Contains(ls, "from-stdin") {
		t.Errorf("%q %v", ls, err)
	}
}

func TestZoneAddTakesNoTokenFlagAndNoEmptyToken(t *testing.T) {
	edgeWildcards = new(gotToken)
	t.Cleanup(func() { edgeWildcards = nil })
	runningEdge(t)
	withStdin(t, "x\n")
	if _, _, err := tnl("zone", "add", "example.com", "--wildcard", "cloudflare", "--token", "x"); err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("--token was accepted: %v", err)
	}
	withStdin(t, "")
	if _, _, err := tnl("zone", "add", "example.com", "--wildcard", "cloudflare"); err == nil || !strings.Contains(err.Error(), "no token") {
		t.Errorf("an empty stdin gave %v", err)
	}
	var _ io.Reader = stdin
}
