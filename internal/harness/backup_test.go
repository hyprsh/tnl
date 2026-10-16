package harness

import (
	"archive/tar"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
)

// extract unpacks a backup archive into dir, as an operator restoring would.
func extract(t *testing.T, r io.Reader, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, filepath.FromSlash(h.Name))
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(p, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}

// An Edge restored from a backup into a fresh state directory, on the same
// --address and WireGuard port, serves every Route with the certificates it
// had, and its Outposts reconnect with the credentials they already hold.
func TestEdgeRestoredFromABackupServesItsRoutesAndOutpostsReconnect(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello from "+r.Host)
	}))
	pb := StartPebble(t, PebbleOptions{})
	re := StartEdgeWith(t, EdgeOptions{Pebble: pb})
	re.AddZone(t, "test")
	nas := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	if resp, body := get(t, re.Caddy, H1, "app.test", "/"); resp.StatusCode != 200 {
		t.Fatalf("before the backup: %d %q", resp.StatusCode, body)
	}

	body, _, err := re.API.Download(ctx(t), api.PathBackup)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := os.MkdirTemp("", "tnl") // short, for tnl.sock
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(restored) })
	extract(t, body, restored)
	body.Close()
	port := re.Port
	re.Stop()
	if err := nas.Stop(); err != nil {
		t.Fatal(err)
	}

	again := StartEdgeWith(t, EdgeOptions{StateDir: restored, WireGuardPort: port, Pebble: pb})
	// The Outpost starts from its own state, with no token: no rekeying.
	again.Connect(t, "nas", "", nas.StateDir)
	resp, text := get(t, again.Caddy, H1, "app.test", "/")
	if resp.StatusCode != 200 || text != "hello from app.test" {
		t.Fatalf("after the restore: %d %q", resp.StatusCode, text)
	}
	if pb.Orders() != 1 {
		t.Errorf("Pebble saw %d orders; the restored certificate should have been used", pb.Orders())
	}
}
