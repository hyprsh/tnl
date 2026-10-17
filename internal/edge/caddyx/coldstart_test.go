package caddyx

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

// Caddy loads a certificate from storage in the background, and a process
// starts with an empty cache, so a Visitor who arrived just after the listener
// opened got "no certificate available" as a TLS internal error. Start returns
// with every unexpired certificate it has in storage already in the cache.
func TestAColdStartReturnsWithTheStoredCertificatesInTheCache(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_DATA_HOME", "")
	httpsPort, httpPort := freePort(t), freePort(t)
	rt := &Runtime{Table: new(routes.Mem), Outposts: new(routes.MemOutposts)}
	Register("cold", rt)
	cfg := Config{
		EdgeID: "cold", StateDir: state,
		HTTPListen: fmt.Sprintf("127.0.0.1:%d", httpPort), HTTPSListen: fmt.Sprintf("127.0.0.1:%d", httpsPort),
		HTTPPort: httpPort, HTTPSPort: httpsPort,
	}
	names := []string{"a.cold.test", "b.cold.test", "c.cold.test"}
	if err := Start(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Stop() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, n := range names {
		if err := rt.Certs().Obtain(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		if err := Stop(); err != nil {
			t.Fatal(err)
		}
		if err := Start(cfg); err != nil {
			t.Fatal(err)
		}
		for _, n := range names {
			if !holdsOwn(n, time.Now()) {
				t.Fatalf("restart %d: Start returned before %s was in the cache", i, n)
			}
		}
	}
}
