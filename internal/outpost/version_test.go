package outpost

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestATokenFromAnEdgeThatWontWorkWithThisOutpostIsRefusedBeforeTheStateIsTouched(t *testing.T) {
	for _, c := range []struct{ edge, outpost string }{
		{"8.0.0", "9.9.9"}, // an Edge of an older major
		{"0.3.4", "0.4.0"}, // before 1.0 the minor must match
		{"2.0.0", "0.9.1"}, // an Outpost two majors behind
	} {
		tok, _ := newToken(t, "127.0.0.1")
		tok.Port = 51820
		tok.EdgeVersion = c.edge
		dir := shortStateDir(t)
		_, err := start(t, Config{Token: tok.Encode(), StateDir: dir, Version: c.outpost})
		var ce *ConfigError
		if !errors.As(err, &ce) || !strings.Contains(err.Error(), c.edge) || !strings.Contains(err.Error(), c.outpost) || !strings.Contains(err.Error(), "install Outpost "+c.edge) {
			t.Errorf("Edge %s, Outpost %s: err = %v", c.edge, c.outpost, err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("Edge %s, Outpost %s: the state directory was touched", c.edge, c.outpost)
		}
	}
}

func writeV1State(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, FileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	json.Unmarshal(raw, &doc)
	delete(doc, "name")
	doc["version"] = 1
	out, _ := json.Marshal(doc)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAnOlderSchemaOfStateIsMigratedAtStartAndTheOldFileKept(t *testing.T) {
	tok, edgeKey := newToken(t, "127.0.0.1")
	edge := newFakeEdge(t, edgeKey, 0, tok.OneTimeKey)
	tok.Port = edge.port
	dir := shortStateDir(t)
	r, err := start(t, Config{Token: tok.Encode(), StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	r.stop()
	edge.tun.Close()
	own := ownKey(t, dir)
	writeV1State(t, dir)
	v1, _ := os.ReadFile(filepath.Join(dir, FileName))

	edge2 := newFakeEdge(t, edgeKey, edge.port, own)
	r, err = start(t, Config{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer r.stop()
	handshake(t, edge2)
	kept, err := os.ReadFile(filepath.Join(dir, "state.json.v1"))
	if err != nil || string(kept) != string(v1) {
		t.Fatalf("kept file %q, %v", kept, err)
	}
	st, err := loadState(dir)
	if err != nil || st.Name == "" {
		t.Fatalf("migrated state %+v, %v", st, err)
	}
	if log := r.log.String(); !strings.Contains(log, "migrated state.json from schema 1 to 2") || !strings.Contains(log, "state.json.v1") {
		t.Errorf("no migration line:\n%s", log)
	}
}

func TestANewerSchemaOfStateIsRefusedAndLeftAlone(t *testing.T) {
	dir := shortStateDir(t)
	os.MkdirAll(dir, 0o700)
	content := `{"version": 3, "name": "x"}`
	path := filepath.Join(dir, FileName)
	os.WriteFile(path, []byte(content), 0o600)
	_, err := start(t, Config{StateDir: dir})
	var ce *ConfigError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), "schema version 3; this tnl knows version 2") {
		t.Fatalf("err = %v", err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != content {
		t.Error("the file changed")
	}
	if _, err := os.Stat(path + ".v3"); !os.IsNotExist(err) {
		t.Error("kept a copy")
	}
}
