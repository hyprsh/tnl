package backup

import (
	"archive/tar"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// certAndKey makes a self-signed certificate for host and its key, as PEM.
func certAndKey(t *testing.T, host string) (crt, key string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
}

func readTar(t *testing.T, b []byte) map[string]string {
	t.Helper()
	got := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return got
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		got[h.Name] = string(body)
	}
}

func names(m map[string]string) []string {
	var out []string
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestArchiveHoldsStateAndCaddyWithoutLocksOrTempFiles(t *testing.T) {
	caddy := filepath.Join(t.TempDir(), "caddy")
	crt, key := certAndKey(t, "app.example.com")
	site := filepath.Join(caddy, "certificates", "acme-v02.api.letsencrypt.org-directory", "app.example.com")
	writeFile(t, filepath.Join(site, "app.example.com.crt"), crt)
	writeFile(t, filepath.Join(site, "app.example.com.key"), key)
	writeFile(t, filepath.Join(site, "app.example.com.json"), "{}")
	writeFile(t, filepath.Join(site, "1234567890"), "half written") // certmagic's temp file
	writeFile(t, filepath.Join(caddy, "acme", "acct", "account.json"), "acct")
	writeFile(t, filepath.Join(caddy, "locks", "issue_cert_app.example.com.lock"), "lock")

	a, err := Prepare(caddy)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Skipped) != 0 {
		t.Fatalf("skipped %v", a.Skipped)
	}
	var buf bytes.Buffer
	if err := a.WriteTo(&buf, []byte(`{"version":1}`)); err != nil {
		t.Fatal(err)
	}
	got := readTar(t, buf.Bytes())
	if got["state.json"] != `{"version":1}` {
		t.Fatalf("state.json = %q", got["state.json"])
	}
	for _, n := range names(got) {
		switch n {
		case "state.json", "caddy/acme/acct/account.json",
			"caddy/certificates/acme-v02.api.letsencrypt.org-directory/app.example.com/app.example.com.crt",
			"caddy/certificates/acme-v02.api.letsencrypt.org-directory/app.example.com/app.example.com.key",
			"caddy/certificates/acme-v02.api.letsencrypt.org-directory/app.example.com/app.example.com.json":
		case "caddy/", "caddy/acme/", "caddy/acme/acct/", "caddy/certificates/",
			"caddy/certificates/acme-v02.api.letsencrypt.org-directory/",
			"caddy/certificates/acme-v02.api.letsencrypt.org-directory/app.example.com/":
		default:
			t.Errorf("unexpected entry %q", n)
		}
	}
	if _, ok := got["caddy/certificates/acme-v02.api.letsencrypt.org-directory/app.example.com/app.example.com.key"]; !ok {
		t.Error("the key is missing")
	}
}

func TestMismatchedCertificateIsLeftOutAndNamed(t *testing.T) {
	caddy := filepath.Join(t.TempDir(), "caddy")
	issuer := filepath.Join(caddy, "certificates", "issuer")
	crt, _ := certAndKey(t, "bad.example.com")
	_, otherKey := certAndKey(t, "other.example.com")
	writeFile(t, filepath.Join(issuer, "bad.example.com", "bad.example.com.crt"), crt)
	writeFile(t, filepath.Join(issuer, "bad.example.com", "bad.example.com.key"), otherKey)
	writeFile(t, filepath.Join(issuer, "bad.example.com", "bad.example.com.json"), "{}")
	goodCrt, goodKey := certAndKey(t, "good.example.com")
	writeFile(t, filepath.Join(issuer, "good.example.com", "good.example.com.crt"), goodCrt)
	writeFile(t, filepath.Join(issuer, "good.example.com", "good.example.com.key"), goodKey)

	a, err := Prepare(caddy)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Skipped) != 1 || a.Skipped[0] != "bad.example.com" {
		t.Fatalf("Skipped = %v", a.Skipped)
	}
	var buf bytes.Buffer
	if err := a.WriteTo(&buf, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	got := readTar(t, buf.Bytes())
	for n := range got {
		if filepath.Base(filepath.Dir(n)) == "bad.example.com" || n == "caddy/certificates/issuer/bad.example.com/" {
			t.Errorf("%q is in the archive", n)
		}
	}
	if _, ok := got["caddy/certificates/issuer/good.example.com/good.example.com.crt"]; !ok {
		t.Error("the good certificate is missing")
	}
}

func TestNoCaddyDirectoryStillMakesAnArchive(t *testing.T) {
	a, err := Prepare(filepath.Join(t.TempDir(), "caddy"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := a.WriteTo(&buf, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if got := names(readTar(t, buf.Bytes())); len(got) != 1 || got[0] != "state.json" {
		t.Fatalf("entries = %v", got)
	}
}
