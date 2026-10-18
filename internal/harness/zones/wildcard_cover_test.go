package zones

import (
	"slices"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"

	. "github.com/hyprsh/tnl/internal/harness"
)

// A wildcard that cannot get its certificate has not come in: the Hostnames it
// would cover keep renewing their own certificates.
func TestAFailedWildcardLeavesTheHostnamesRenewingTheirOwn(t *testing.T) {
	svc := NewHTTPService(t, hello())
	fake := StartFakeDNS(t, "example.test", dnsToken)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), CertWait: 2 * time.Second})
	re.AddZone(t, "example.test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.example.test", "http://"+svc.Addr(), "nas")
	fake.FailChallenges()

	r, err := zoneEdit(t, re, api.ZoneEditRequest{Name: "example.test", Wildcard: "fake", Token: dnsToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings.Warnings) == 0 {
		t.Fatalf("no warning for a wildcard without a certificate: %+v", r)
	}
	if re.Certs.Managing() != 1 {
		t.Errorf("managing %d Hostnames, want app.example.test still", re.Certs.Managing())
	}
}

// Removing a wildcard Zone whose Route moves to a per-Hostname parent returns
// only once the Route has a certificate of its own.
func TestZoneRmOfAWildcardZoneWaitsForTheMovedRoutesOwnCertificates(t *testing.T) {
	svc := NewHTTPService(t, hello())
	StartFakeDNS(t, "example.test", dnsToken)
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "example.test")
	re.JoinOutpost(t, "nas")
	if _, err := zoneAddWildcard(t, re, "lab.example.test", "fake", dnsToken); err != nil {
		t.Fatal(err)
	}
	re.AddRoute(t, "app.lab.example.test", "http://"+svc.Addr(), "nas")
	v := startPoller(t, re, "app.lab.example.test")

	r, err := zoneRm(t, re, "lab.example.test", false)
	if err != nil || len(r.Moved) != 1 || len(r.Warnings.Warnings) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if names := servedNames(t, re, "app.lab.example.test"); !slices.Contains(names, "app.lab.example.test") {
		t.Errorf("served with %v right after zone rm, not a certificate of its own", names)
	}
	if n, fails := v.finish(); len(fails) != 0 {
		t.Fatalf("%d of %d handshakes failed, first: %s", len(fails), n, fails[0])
	}
}

// A restarted Edge finds the wildcard's certificates in storage and orders
// nothing for the Hostnames they cover, not even for the moments before the
// first Tick would settle them.
func TestARestartOrdersNothingForHostnamesAStoredWildcardCovers(t *testing.T) {
	StartFakeDNS(t, "example.test", dnsToken)
	pb := StartPebble(t, PebbleOptions{})
	re := StartEdgeWith(t, EdgeOptions{Pebble: pb})
	re.JoinOutpost(t, "nas")
	if _, err := zoneAddWildcard(t, re, "lab.example.test", "fake", dnsToken); err != nil {
		t.Fatal(err)
	}
	re.AddRoute(t, "app.lab.example.test", "http://127.0.0.1:1", "nas")
	re.AddRoute(t, "lab.example.test", "http://127.0.0.1:1", "nas")
	orders := pb.Orders()
	port, dir := re.Port, re.StateDir
	re.Stop()

	again := StartEdgeWith(t, EdgeOptions{StateDir: dir, WireGuardPort: port, Pebble: pb})
	time.Sleep(3 * time.Second)
	if n := pb.Orders(); n != orders {
		t.Errorf("the restart made %d orders for covered Hostnames", n-orders)
	}
	if again.Certs.Managing() != 0 {
		t.Errorf("managing %d covered Hostnames of their own", again.Certs.Managing())
	}
}
