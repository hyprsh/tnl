package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/keys"
)

// schema2 is a state.json as every 0.1.x Edge wrote it: Routes and a declared
// line, none with no_hsts.
func schema2(t *testing.T) string {
	t.Helper()
	priv, _, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return `{
  "version": 2,
  "private_key": "` + keyText(priv) + `",
  "next_address": 2,
  "outposts": [{"name": "home", "public_key": "` + keyText(mustKey(t)) + `", "address": "` + "100.64.216.4" + `", "declared": true}],
  "routes": [
    {"hostname": "app.example.com", "outpost": "home", "service": "http://localhost:3000"},
    {"hostname": "b.example.com", "outpost": "home", "service": "http://localhost:3001", "host_header": "b.lan"}
  ],
  "zones": [{"name": "example.com", "mode": "per-hostname"}],
  "declarations": [{"outpost": "home", "routes": [
    {"hostname": "app.example.com", "service": "http://localhost:3000", "declared": "2026-01-02T03:04:05Z"},
    {"hostname": "b.example.com", "service": "http://localhost:3001", "host_header": "b.lan", "declared": "2026-01-02T03:04:05Z"}
  ]}]
}
`
}

func TestASchema2StateMigratesWithHSTSOffAndKeepsTheOldFile(t *testing.T) {
	dir := t.TempDir()
	old := schema2(t)
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, log := open(t, dir)
	st := s.Snapshot()
	if st.Version != Version || len(st.Routes) != 2 {
		t.Fatalf("version %d, %d routes", st.Version, len(st.Routes))
	}
	for _, r := range st.Routes {
		if !r.NoHSTS {
			t.Errorf("Route %s has HSTS on", r.Hostname)
		}
	}
	if l := st.DeclarationOf("home"); len(l) != 2 || !l[0].NoHSTS || !l[1].NoHSTS {
		t.Errorf("declared lines: %+v", l)
	}
	kept, err := os.ReadFile(filepath.Join(dir, FileName+".v2"))
	if err != nil || string(kept) != old {
		t.Fatalf("state.json.v2: %v, kept as it was: %v", err, string(kept) == old)
	}
	if !strings.Contains(log.String(), "migrated") || !strings.Contains(log.String(), "level=INFO") {
		t.Errorf("no info log of the migration:\n%s", log)
	}
	s2, _ := open(t, dir) // the file on disk is schema 3 now
	if s2.Snapshot().Version != Version {
		t.Fatal("reopened at the wrong schema")
	}
}

func TestAMigratedStateKeepsTheOptionsItHad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(schema2(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := open(t, dir)
	r, _ := s.Snapshot().Route("b.example.com")
	if r.HostHeader != "b.lan" || r.Service != "http://localhost:3001" {
		t.Fatalf("%+v", r)
	}
}
