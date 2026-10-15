package harness

import (
	"crypto/x509"
	"encoding/pem"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/letsencrypt/pebble/v2/ca"
	"github.com/letsencrypt/pebble/v2/db"
	"github.com/letsencrypt/pebble/v2/va"
	"github.com/letsencrypt/pebble/v2/wfe"
)

// PebbleOptions tunes a Pebble.
type PebbleOptions struct {
	// Validate makes Pebble really check challenges. By default it validates
	// nothing (PEBBLE_VA_ALWAYS_VALID=1). With it set, HTTP-01 challenges go to
	// Pebble.ValidationPort on the Hostname, so the Edge must listen there:
	// pass the Pebble in CaddyOptions and StartCaddy does.
	Validate bool
}

// Pebble is an in-process ACME CA (Let's Encrypt's test server).
type Pebble struct {
	// DirectoryURL is the ACME directory.
	DirectoryURL string
	// Root is the root every certificate Pebble issues chains to.
	Root *x509.Certificate
	// ValidationPort is where HTTP-01 validation connects, if Validate is set.
	ValidationPort int

	opts     PebbleOptions
	tlsCert  *x509.Certificate
	orders   atomic.Int64
	certFile string
}

// pebbleEnv serialises the environment Pebble reads when it is built.
var pebbleEnv sync.Mutex

// StartPebble starts a Pebble that stops when the test ends. It reads PEBBLE_*
// environment variables only while it is built, so it never leaks them.
func StartPebble(t testing.TB, opts PebbleOptions) *Pebble {
	t.Helper()
	p := &Pebble{opts: opts}
	p.ValidationPort = freePort(t, false)

	lg := log.New(io.Discard, "", 0)
	mem := db.NewMemoryStore()
	cai := ca.New(lg, mem, "", "ecdsa", 0, 1, map[string]ca.Profile{"default": {Description: "default"}})

	pebbleEnv.Lock()
	restore := setenv(map[string]string{
		"PEBBLE_VA_ALWAYS_VALID": map[bool]string{true: "0", false: "1"}[opts.Validate],
		"PEBBLE_VA_NOSLEEP":      "1",
		"PEBBLE_WFE_NONCEREJECT": "0",
	})
	vai := va.New(lg, p.ValidationPort, 0, false, "", mem)
	w := wfe.New(lg, mem, vai, cai, []string{"pebble.letsencrypt.org"}, false, false, 3, 5)
	restore()
	pebbleEnv.Unlock()

	handler := w.Handler()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/order-plz") {
			p.orders.Add(1)
		}
		handler.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)
	p.DirectoryURL = srv.URL + "/dir"
	p.Root = cai.GetRootCert(0).Cert
	p.tlsCert = srv.Certificate()

	p.certFile = filepath.Join(t.TempDir(), "pebble-tls.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.tlsCert.Raw})
	if err := os.WriteFile(p.certFile, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Orders is how many certificate orders Pebble has received: how often the Edge
// asked for issuance.
func (p *Pebble) Orders() int { return int(p.orders.Load()) }

func setenv(kv map[string]string) (restore func()) {
	type old struct {
		v  string
		ok bool
	}
	saved := map[string]old{}
	for k, v := range kv {
		o, ok := os.LookupEnv(k)
		saved[k] = old{o, ok}
		os.Setenv(k, v)
	}
	return func() {
		for k, o := range saved {
			if o.ok {
				os.Setenv(k, o.v)
			} else {
				os.Unsetenv(k)
			}
		}
	}
}
