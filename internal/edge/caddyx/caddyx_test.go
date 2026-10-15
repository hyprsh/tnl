package caddyx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddytls"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestConfigSetsCaddyAsAWhole(t *testing.T) {
	b, err := Config{EdgeID: "e", StateDir: "/var/lib/tnl"}.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Admin struct {
			Disabled bool `json:"disabled"`
			Config   struct {
				Persist *bool `json:"persist"`
			} `json:"config"`
		} `json:"admin"`
		Storage struct {
			Module string `json:"module"`
			Root   string `json:"root"`
		} `json:"storage"`
		Apps struct {
			HTTP struct {
				GracePeriod string `json:"grace_period"`
				Servers     map[string]struct {
					Listen []string `json:"listen"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Admin.Disabled || cfg.Admin.Config.Persist == nil || *cfg.Admin.Config.Persist {
		t.Errorf("admin = %+v, want disabled with persist false", cfg.Admin)
	}
	if cfg.Storage.Module != "file_system" || cfg.Storage.Root != "/var/lib/tnl/caddy" {
		t.Errorf("storage = %+v", cfg.Storage)
	}
	if d, err := time.ParseDuration(cfg.Apps.HTTP.GracePeriod); err != nil || d != 0 {
		t.Errorf("grace_period = %q, want 0", cfg.Apps.HTTP.GracePeriod)
	}
	if l := cfg.Apps.HTTP.Servers["https"].Listen; len(l) != 1 || l[0] != ":443" {
		t.Errorf("https listens on %v", l)
	}
	if l := cfg.Apps.HTTP.Servers["redirect"].Listen; len(l) != 1 || l[0] != ":80" {
		t.Errorf("redirect listens on %v", l)
	}
}

func TestLoadedCaddyHasTheSpecsServerSettings(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_DATA_HOME", "")
	httpsPort, httpPort := freePort(t), freePort(t)
	Register("settings", &Runtime{Table: new(routes.Mem), Outposts: new(routes.MemOutposts)})
	if err := Start(Config{
		EdgeID: "settings", StateDir: state,
		HTTPListen: fmt.Sprintf("127.0.0.1:%d", httpPort), HTTPSListen: fmt.Sprintf("127.0.0.1:%d", httpsPort),
		HTTPPort: httpPort, HTTPSPort: httpsPort,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Stop() })

	app, err := caddy.ActiveContext().App("http")
	if err != nil {
		t.Fatal(err)
	}
	httpApp := app.(*caddyhttp.App)
	if httpApp.GracePeriod != 0 {
		t.Errorf("grace_period = %v", httpApp.GracePeriod)
	}
	s := httpApp.Servers["https"]
	if s == nil {
		t.Fatal("no https server")
	}
	for _, c := range []struct {
		name string
		got  caddy.Duration
		want time.Duration
	}{
		{"read_timeout", s.ReadTimeout, 0},
		{"write_timeout", s.WriteTimeout, 0},
		{"read_header_timeout", s.ReadHeaderTimeout, time.Minute},
		{"idle_timeout", s.IdleTimeout, 5 * time.Minute},
		{"read_idle_timeout", s.ReadIdleTimeout, 5 * time.Minute},
		{"write_idle_timeout", s.WriteIdleTimeout, time.Minute},
	} {
		if time.Duration(c.got) != c.want {
			t.Errorf("%s = %v, want %v", c.name, time.Duration(c.got), c.want)
		}
	}
	if fmt.Sprint(s.Protocols) != "[h1 h2 h3]" {
		t.Errorf("protocols = %v, want h1 h2 h3", s.Protocols)
	}
	// HTTP/3 is on: something holds the UDP port next to the TCP one.
	if pc, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", httpsPort)); err == nil {
		pc.Close()
		t.Error("nothing listens for HTTP/3 on the HTTPS port over UDP")
	}

	// Storage and the data directory sit under StateDir/caddy.
	if got, want := caddy.AppDataDir(), filepath.Join(state, "caddy"); got != want {
		t.Errorf("data dir = %s, want %s", got, want)
	}
	if _, err := os.Stat(RootCertPath(state)); err != nil {
		t.Errorf("Caddy's CA is not under the state directory: %v", err)
	}
}

func TestTransportSettings(t *testing.T) {
	rt := &Runtime{}
	route := routes.Route{Hostname: "app.test", Outpost: "o", Service: "s:1"}
	addr := netip.MustParseAddrPort("100.64.0.2:7000")
	tr := rt.pool.transport(rt, route, addr)
	if tr.Protocols == nil || !tr.Protocols.HTTP1() || tr.Protocols.HTTP2() || tr.Protocols.UnencryptedHTTP2() {
		t.Errorf("protocols = %v, want HTTP/1.1 only", tr.Protocols)
	}
	if !tr.DisableCompression {
		t.Error("compression is not disabled")
	}
	if tr.ResponseHeaderTimeout != 0 {
		t.Errorf("ResponseHeaderTimeout = %v, want none", tr.ResponseHeaderTimeout)
	}
	if tr.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 90s", tr.IdleConnTimeout)
	}
	if rt.pool.transport(rt, route, addr) != tr {
		t.Error("a Route got a second transport; connections would not pool per Route")
	}
	other := routes.Route{Hostname: "other.test", Outpost: "o", Service: "s:1"}
	if rt.pool.transport(rt, other, addr) == tr {
		t.Error("two Routes share a transport")
	}
}

func issuerOf(t *testing.T, c Config) (policy map[string]any, onDemand map[string]any) {
	t.Helper()
	b, err := c.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Apps struct {
			TLS struct {
				Automation struct {
					OnDemand map[string]any   `json:"on_demand"`
					Policies []map[string]any `json:"policies"`
				} `json:"automation"`
			} `json:"tls"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	a := cfg.Apps.TLS.Automation
	if len(a.Policies) != 1 {
		t.Fatalf("policies = %v", a.Policies)
	}
	return a.Policies[0], a.OnDemand
}

func TestACMEDefaultsToLetsEncryptProduction(t *testing.T) {
	p, od := issuerOf(t, Config{EdgeID: "e", StateDir: "/s", ACME: &ACME{}})
	iss := p["issuers"].([]any)[0].(map[string]any)
	if iss["module"] != "acme" || iss["ca"] != "https://acme-v02.api.letsencrypt.org/directory" {
		t.Errorf("issuer = %v", iss)
	}
	if _, ok := iss["email"]; ok {
		t.Errorf("email set without one: %v", iss)
	}
	if p["on_demand"] != true {
		t.Errorf("policy = %v, want on_demand", p)
	}
	perm, _ := od["permission"].(map[string]any)
	if perm["module"] != "tnl" || perm["edge"] != "e" {
		t.Errorf("permission = %v, want the tnl module for this Edge", od)
	}
}

func TestACMETakesCAAndEmail(t *testing.T) {
	p, _ := issuerOf(t, Config{EdgeID: "e", StateDir: "/s", ACME: &ACME{CA: "https://ca.test/dir", Email: "ops@example.com"}})
	iss := p["issuers"].([]any)[0].(map[string]any)
	if iss["ca"] != "https://ca.test/dir" || iss["email"] != "ops@example.com" {
		t.Errorf("issuer = %v", iss)
	}
}

func TestWithoutACMEThePolicyUsesTheInternalIssuer(t *testing.T) {
	p, _ := issuerOf(t, Config{EdgeID: "e", StateDir: "/s"})
	iss := p["issuers"].([]any)[0].(map[string]any)
	if iss["module"] != "internal" {
		t.Errorf("issuer = %v", iss)
	}
}

func TestPermissionRefusesHostnamesWithoutARoute(t *testing.T) {
	tbl := new(routes.Mem)
	tbl.Swap([]routes.Route{{Hostname: "app.test", Outpost: "o", Service: "s:1"}})
	p := &Permission{Edge: "perm"}
	unreg := Register("perm", &Runtime{Table: tbl})
	defer unreg()
	if err := p.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if err := p.CertificateAllowed(context.Background(), "App.test"); err != nil {
		t.Errorf("routed Hostname refused: %v", err)
	}
	err := p.CertificateAllowed(context.Background(), "nope.test")
	if !errors.Is(err, caddytls.ErrPermissionDenied) {
		t.Errorf("err = %v, want it to wrap caddytls.ErrPermissionDenied", err)
	}
}
