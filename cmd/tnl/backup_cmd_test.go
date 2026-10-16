//go:build !outpost

package main

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarNames(t *testing.T, b []byte) []string {
	t.Helper()
	var names []string
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
}

func TestBackupToStdoutWritesATar(t *testing.T) {
	runningEdge(t)
	out, code, err := tnl("backup", "-")
	if err != nil || code != 0 {
		t.Fatalf("%v %d", err, code)
	}
	if names := tarNames(t, []byte(out)); len(names) == 0 || names[0] != "state.json" {
		t.Fatalf("entries %v", names)
	}
}

func TestBackupToFileIsMode0600(t *testing.T) {
	runningEdge(t)
	file := filepath.Join(t.TempDir(), "edge.tar")
	out, _, err := tnl("backup", file)
	if err != nil || out != "" {
		t.Fatalf("%q %v", out, err)
	}
	fi, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	b, _ := os.ReadFile(file)
	if names := tarNames(t, b); len(names) == 0 || names[0] != "state.json" {
		t.Fatalf("entries %v", names)
	}
	if left, _ := filepath.Glob(file + ".*"); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestBackupNamesLeftOutCertificatesOnStderr(t *testing.T) {
	runningEdge(t)
	dir := os.Getenv("TNL_STATE_DIR")
	site := filepath.Join(dir, "caddy", "certificates", "issuer", "bad.example.com")
	if err := os.MkdirAll(site, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(site, "bad.example.com.crt"), []byte("not a cert"), 0o600)
	os.WriteFile(filepath.Join(site, "bad.example.com.key"), []byte("not a key"), 0o600)
	var errBuf bytes.Buffer
	old := stderr
	stderr = &errBuf
	defer func() { stderr = old }()
	out, _, err := tnl("backup", "-")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "bad.example.com") {
		t.Fatalf("stderr %q does not name the certificate", errBuf.String())
	}
	for _, n := range tarNames(t, []byte(out)) {
		if strings.Contains(n, "bad.example.com") {
			t.Fatalf("%q is in the archive", n)
		}
	}
}

func TestBackupUsage(t *testing.T) {
	runningEdge(t)
	if _, code, err := tnl("backup"); err == nil || code == 0 {
		t.Fatalf("%v %d", err, code)
	}
}
