package server_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
)

// fakeCerts is the certificate manager as route add sees it: Obtain waits on
// gate, or answers err.
type fakeCerts struct {
	mu    sync.Mutex
	asked []string
	gate  chan struct{}
	err   error
}

func (c *fakeCerts) Obtain(ctx context.Context, hostname string) error {
	c.mu.Lock()
	c.asked = append(c.asked, hostname)
	c.mu.Unlock()
	if c.gate != nil {
		select {
		case <-c.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return c.err
}

func (c *fakeCerts) askedFor() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.asked...)
}

func withCerts(c server.Certificates, wait time.Duration) func(*server.Config) {
	return func(cfg *server.Config) { cfg.Certs, cfg.CertWait = c, wait }
}

func certFixture(t *testing.T, c server.Certificates, wait time.Duration) *fixture {
	t.Helper()
	f := startConfigured(t, "1.2.3", api.ActorLocal, func(cfg *server.Config) {
		cfg.Resolver = edgeResolver()
		withCerts(c, wait)(cfg)
	})
	for _, z := range []string{"example.com", "points-away.test", "nowhere.test"} {
		if _, err := addZone(f.client, z); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := addOutpost(f.client, "nas"); err != nil {
		t.Fatal(err)
	}
	return f
}

// route add replies only once the certificate is held, and the wait is after
// the lock: another change goes through meanwhile.
func TestRouteAddWaitsForTheCertificateAfterTheLock(t *testing.T) {
	c := &fakeCerts{gate: make(chan struct{})}
	f := certFixture(t, c, time.Minute)

	replied := make(chan error, 1)
	go func() {
		_, err := addRoute(f.client, "app.example.com", "http://localhost:3000", "nas")
		replied <- err
	}()

	deadline := time.Now().Add(5 * time.Second)
	for len(c.askedFor()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := c.askedFor(); len(got) != 1 || got[0] != "app.example.com" {
		t.Fatalf("certificate asked for %q", got)
	}
	if _, err := addOutpost(f.client, "laptop"); err != nil {
		t.Fatalf("a change during the wait: %v", err)
	}
	select {
	case err := <-replied:
		t.Fatalf("route add replied before the certificate: %v", err)
	default:
	}
	close(c.gate)
	if err := <-replied; err != nil {
		t.Fatal(err)
	}
}

func TestRouteAddWarnsWhenTheCertificateFailsAndKeepsTheRoute(t *testing.T) {
	c := &fakeCerts{err: errors.New("the CA said no")}
	f := certFixture(t, c, time.Minute)
	r, err := addRoute(f.client, "app.example.com", "http://localhost:3000", "nas")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings.Warnings) != 1 {
		t.Fatalf("warnings %q", r.Warnings.Warnings)
	}
	w := r.Warnings.Warnings[0]
	for _, frag := range []string{"app.example.com", "the CA said no", "keeps retrying"} {
		if !strings.Contains(w, frag) {
			t.Errorf("warning %q lacks %q", w, frag)
		}
	}
	if len(f.store.Snapshot().Routes) != 1 {
		t.Error("the Route was not saved")
	}
	if len(logLines(f, "level=warn")) == 0 || !strings.Contains(strings.Join(logLines(f, "level=warn"), "\n"), "the CA said no") {
		t.Errorf("the Edge did not log the warning:\n%s", f.log.String())
	}
}

func TestRouteAddWaitEndsAtTheLimitWithAWarning(t *testing.T) {
	c := &fakeCerts{gate: make(chan struct{})}
	f := certFixture(t, c, 100*time.Millisecond)
	start := time.Now()
	r, err := addRoute(f.client, "app.example.com", "http://localhost:3000", "nas")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second || len(r.Warnings.Warnings) != 1 || !strings.Contains(r.Warnings.Warnings[0], "keeps retrying") {
		t.Fatalf("after %v: warnings %q", time.Since(start), r.Warnings.Warnings)
	}
}

func TestRouteAddWarnsWhenTheHostnameDoesNotPointAtTheEdge(t *testing.T) {
	for host, frag := range map[string]string{
		"app.points-away.test": "203.0.113.9",
		"app.nowhere.test":     "doesn't resolve",
	} {
		f := certFixture(t, &fakeCerts{}, time.Minute)
		r, err := addRoute(f.client, host, "http://localhost:3000", "nas")
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Warnings.Warnings) != 1 || !strings.Contains(r.Warnings.Warnings[0], host) || !strings.Contains(r.Warnings.Warnings[0], frag) {
			t.Errorf("%s: warnings %q", host, r.Warnings.Warnings)
		}
		if len(f.store.Snapshot().Routes) != 1 {
			t.Errorf("%s: the Route was not saved", host)
		}
	}
}

func TestRouteAddToAHostnamePointingAtTheEdgeHasNoWarnings(t *testing.T) {
	f := certFixture(t, &fakeCerts{}, time.Minute)
	r, err := addRoute(f.client, "app.example.com", "http://localhost:3000", "nas")
	if err != nil {
		t.Fatal(err)
	}
	if r.Warnings.Warnings != nil {
		t.Fatalf("warnings %q", r.Warnings.Warnings)
	}
}
