package statefile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeJSON(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

var addName = Steps{1: func(m map[string]any) error {
	m["name"] = "stand-in"
	return nil
}}

func TestMigrateLeavesTheCurrentSchemaAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	writeJSON(t, path, `{"version": 2, "name": "a"}`)
	from, err := Migrate(path, "state.json", 2, addName)
	if err != nil || from != 0 {
		t.Fatalf("from %d, err %v", from, err)
	}
	if _, err := os.Stat(path + ".v2"); !os.IsNotExist(err) {
		t.Fatal("kept a copy of a file it did not migrate")
	}
}

func TestMigrateUpgradesAnOlderSchemaAndKeepsTheOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	old := `{"version": 1, "port": 51820}`
	writeJSON(t, path, old)
	from, err := Migrate(path, "state.json", 2, addName)
	if err != nil || from != 1 {
		t.Fatalf("from %d, err %v", from, err)
	}
	kept, err := os.ReadFile(path + ".v1")
	if err != nil || string(kept) != old {
		t.Fatalf("kept file %q, %v", kept, err)
	}
	raw, _ := os.ReadFile(path)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != 2.0 || got["name"] != "stand-in" || got["port"] != 51820.0 {
		t.Fatalf("migrated file %s", raw)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
}

func TestMigrateRefusesASchemaItDoesNotKnow(t *testing.T) {
	for name, content := range map[string]string{
		"newer":        `{"version": 3}`,
		"no step":      `{"version": 0}`,
		"not JSON":     `nope`,
		"no version":   `{}`,
		"negative one": `{"version": -1}`,
	} {
		path := filepath.Join(t.TempDir(), "state.json")
		writeJSON(t, path, content)
		_, err := Migrate(path, "state.json", 2, addName)
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		if raw, _ := os.ReadFile(path); string(raw) != content {
			t.Errorf("%s: the file changed", name)
		}
		if name == "newer" && !strings.Contains(err.Error(), "schema version 3; this tnl knows version 2") {
			t.Errorf("newer: %v", err)
		}
	}
}
