package harness

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

	"github.com/hyprsh/tnl/internal/edge/caddyx"
)

// Proto is the HTTP version a Visitor speaks.
type Proto string

const (
	H1 Proto = "HTTP/1.1"
	H2 Proto = "HTTP/2.0"
	H3 Proto = "HTTP/3.0"
)

// Protos lists every Visitor protocol.
var Protos = []Proto{H1, H2, H3}

// CaddyOptions tunes the Caddy side; the zero value is the spec's settings.
type CaddyOptions struct {
	Timeouts *caddyx.Timeouts
	// Pebble makes Caddy get its certificates from this ACME CA, on demand,
	// instead of from its internal CA. CaddyEdge.Roots is then Pebble's root.
	Pebble *Pebble
	// Log receives Caddy's entries through tnl's log core. Nil means slog.Default().
	Log *slog.Logger
	// RenewCheckInterval shortens Caddy's scan for certificates to renew.
	RenewCheckInterval time.Duration
	// AccessLog logs successful requests too, as --access-log does.
	AccessLog bool
	// InZone says whether a Hostname lies inside a Zone; nil says yes.
	InZone func(host string) bool
}

// CaddyEdge is the running Caddy side of the Edge. Caddy is a process singleton,
// so a test binary runs one at a time and its tests must not be parallel.
type CaddyEdge struct {
	// HTTPPort and HTTPSPort are the loopback ports Visitors use. HTTP/3 uses
	// HTTPSPort over UDP.
	HTTPPort, HTTPSPort int
	// StateDir is a temp directory; Caddy keeps its files in StateDir/caddy.
	StateDir string
	// Roots holds Caddy's internal CA root, which signed every certificate.
	Roots *x509.CertPool

	cfg caddyx.Config
	rt  *caddyx.Runtime
}

// Certs is the Edge's certificate manager.
func (ce *CaddyEdge) Certs() *caddyx.Certs { return ce.rt.Certs() }

// StartCaddy starts Caddy in front of the Edge's Route table and Tunnel, with
// Certificates from Caddy's internal CA. It stops when the test ends.
func (e *Edge) StartCaddy(t testing.TB, opts CaddyOptions) *CaddyEdge {
	t.Helper()
	var id [8]byte
	rand.Read(id[:])
	edgeID := hex.EncodeToString(id[:])
	rt := &caddyx.Runtime{Table: e.Routes, Outposts: e.Outposts, Log: opts.Log, AccessLog: opts.AccessLog, InZone: opts.InZone, Dialer: func(ctx context.Context, a netip.AddrPort) (net.Conn, error) {
		return e.Net.DialContextTCPAddrPort(ctx, a)
	}}
	unregister := caddyx.Register(edgeID, rt)

	httpsPort := freePort(t, true)
	httpPort := freePort(t, false)
	if opts.Pebble != nil && opts.Pebble.opts.Validate {
		// Pebble's HTTP-01 validation connects to this port.
		httpPort = opts.Pebble.ValidationPort
	}
	ce := &CaddyEdge{HTTPPort: httpPort, HTTPSPort: httpsPort, StateDir: t.TempDir(), rt: rt}
	// Caddy's data directory comes from the environment; put it back afterwards.
	if old, ok := os.LookupEnv("XDG_DATA_HOME"); ok {
		t.Cleanup(func() { os.Setenv("XDG_DATA_HOME", old) })
	} else {
		t.Cleanup(func() { os.Unsetenv("XDG_DATA_HOME") })
	}
	ce.cfg = caddyx.Config{
		EdgeID:      edgeID,
		StateDir:    ce.StateDir,
		HTTPListen:  fmt.Sprintf("127.0.0.1:%d", httpPort),
		HTTPSListen: fmt.Sprintf("127.0.0.1:%d", httpsPort),
		HTTPPort:    httpPort,
		HTTPSPort:   httpsPort,
		Timeouts:    opts.Timeouts,

		RenewCheckInterval: opts.RenewCheckInterval,
	}
	if p := opts.Pebble; p != nil {
		ce.cfg.ACME = &caddyx.ACME{CA: p.DirectoryURL, Email: "ops@example.com", TrustedRoots: []string{p.certFile}}
	}
	t.Cleanup(func() {
		caddyx.Stop()
		if rt.Certs().Managing() > 0 {
			// certmagic's jobs finish their storage writes a moment after Stop,
			// releasing their lock files last, and would race the removal of the
			// temp directory. Wait until no lock file is left; the cap keeps a
			// lock that is never released from stalling the test.
			waitNoLocks(filepath.Join(ce.StateDir, "caddy", "locks"), time.Second)
		}
		unregister()
	})
	if err := caddyx.Start(ce.cfg); err != nil {
		t.Fatal(err)
	}
	if opts.Pebble != nil {
		ce.Roots = x509.NewCertPool()
		ce.Roots.AddCert(opts.Pebble.Root)
		e.Caddy = ce
		return ce
	}
	pem, err := os.ReadFile(caddyx.RootCertPath(ce.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	ce.Roots = x509.NewCertPool()
	if !ce.Roots.AppendCertsFromPEM(pem) {
		t.Fatal("no certificate in Caddy's root.crt")
	}
	e.Caddy = ce
	return ce
}

// Reload loads the same Caddy config again, which restarts every module
// instance. Running streams must survive it.
func (ce *CaddyEdge) Reload(t testing.TB) {
	t.Helper()
	if err := caddyx.Start(ce.cfg); err != nil {
		t.Fatal(err)
	}
}

// freePort finds a port free on TCP, and on UDP too if udp is set.
func freePort(t testing.TB, udp bool) int {
	t.Helper()
	for range 50 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		if !udp {
			return port
		}
		pc, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			pc.Close()
			return port
		}
	}
	t.Fatal("no free port")
	return 0
}

// Client returns an HTTP client that speaks proto to the Edge's HTTPS port for
// whatever host the URL names, with the right SNI and trusting Caddy's root. It
// follows no redirects and has no timeout, so pass a context to each request.
func (ce *CaddyEdge) Client(t testing.TB, proto Proto) *http.Client {
	t.Helper()
	tc := &tls.Config{RootCAs: ce.Roots}
	addr := fmt.Sprintf("127.0.0.1:%d", ce.HTTPSPort)
	var rt http.RoundTripper
	switch proto {
	case H3:
		tr := &http3.Transport{
			TLSClientConfig: tc,
			Dial: func(ctx context.Context, _ string, c *tls.Config, q *quic.Config) (*quic.Conn, error) {
				return quic.DialAddrEarly(ctx, addr, c, q)
			},
		}
		t.Cleanup(func() { tr.Close() })
		rt = tr
	case H1, H2:
		var p http.Protocols
		if proto == H2 {
			p.SetHTTP2(true)
		} else {
			p.SetHTTP1(true)
		}
		tr := &http.Transport{
			TLSClientConfig: tc, Protocols: &p,
			DialContext: func(ctx context.Context, n, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, n, addr)
			},
		}
		t.Cleanup(tr.CloseIdleConnections)
		rt = tr
	default:
		t.Fatalf("unknown protocol %q", proto)
	}
	return &http.Client{
		Transport:     rt,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// PlainClient returns a client for the Edge's port 80, for any host.
func (ce *CaddyEdge) PlainClient() *http.Client {
	addr := fmt.Sprintf("127.0.0.1:%d", ce.HTTPPort)
	return &http.Client{
		Transport: &http.Transport{DialContext: func(ctx context.Context, n, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, n, addr)
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func verifyOpts(p *Pebble, chain []*x509.Certificate) x509.VerifyOptions {
	roots := x509.NewCertPool()
	roots.AddCert(p.Root)
	inter := x509.NewCertPool()
	for _, c := range chain {
		inter.AddCert(c)
	}
	return x509.VerifyOptions{Roots: roots, Intermediates: inter}
}

// waitNoLocks waits, for at most max, until dir holds no certmagic lock file.
func waitNoLocks(dir string, max time.Duration) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if entries, err := os.ReadDir(dir); err != nil || len(entries) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
