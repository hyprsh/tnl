package harness

import (
	"context"
	"testing"
	"time"
)

// Caddy suite: a Route Hostname handed to the certificate manager is renewed in
// the background, with no handshake asking for it, and the manager hears
// cert_obtained for the first certificate and again for the renewal.
func TestCaddySuiteRouteHostnameRenewsInTheBackground(t *testing.T) {
	h := New(t)
	// A certificate that lives 30 s is renewed when a third of it is left, and
	// Caddy looks for renewals every second.
	pb := StartPebble(t, PebbleOptions{Validity: 30 * time.Second})
	ce := h.Edge.StartCaddy(t, CaddyOptions{Pebble: pb, RenewCheckInterval: time.Second})

	if err := ce.Certs().Manage("app.test"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first certificate", 30*time.Second, func() bool {
		st, _ := ce.Certs().Status("app.test")
		return st.Obtained >= 1
	})
	first, _ := ce.Certs().Status("app.test")
	waitFor(t, "the background renewal", 50*time.Second, func() bool {
		st, _ := ce.Certs().Status("app.test")
		return st.Obtained >= 2
	})
	renewed, _ := ce.Certs().Status("app.test")
	if !renewed.NotAfter.After(first.NotAfter) {
		t.Errorf("renewed certificate expires %v, not after the first's %v", renewed.NotAfter, first.NotAfter)
	}
	if n := pb.Orders(); n < 2 {
		t.Errorf("Pebble saw %d orders, want the renewal's too", n)
	}
}

// Caddy suite: cert_failed reaches the manager when the CA refuses, a reload
// hands the Hostname over again, and cert_obtained then arrives.
func TestCaddySuiteBothCertificateEventsReachTheManager(t *testing.T) {
	h := New(t)
	pb := StartPebble(t, PebbleOptions{})
	ce := h.Edge.StartCaddy(t, CaddyOptions{Pebble: pb})

	pb.Refuse(true)
	if err := ce.Certs().Manage("app.test"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "cert_failed", 30*time.Second, func() bool {
		st, _ := ce.Certs().Status("app.test")
		return st.Failed >= 1
	})
	if st, _ := ce.Certs().Status("app.test"); st.LastError == "" || st.Failures < 1 {
		t.Errorf("status after a refusal: %+v", st)
	}

	pb.Refuse(false)
	ce.Reload(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := ce.Certs().Wait(ctx, "app.test"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "cert_obtained", 5*time.Second, func() bool {
		st, _ := ce.Certs().Status("app.test")
		return st.Obtained >= 1 && st.Failures == 0
	})
}
