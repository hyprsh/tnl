package statefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReplacesTheFileWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	for _, content := range []string{"first", "second, longer"} {
		if err := Write(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != content {
			t.Fatalf("read %q, %v; want %q", got, err, content)
		}
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file left behind: %v", err)
	}
}

func TestWriteFailsWhenTheDirectoryIsMissing(t *testing.T) {
	if err := Write(filepath.Join(t.TempDir(), "missing", "state.json"), []byte("x")); err == nil {
		t.Fatal("wrote into a directory that does not exist")
	}
}

func TestDecodeChecksTheVersionBeforeTheFields(t *testing.T) {
	type doc struct {
		Version int    `json:"version"`
		Name    string `json:"name"`
	}
	var d doc
	if err := Decode([]byte(`{"version":1,"name":"a"}`), "state.json", 1, &d); err != nil || d.Name != "a" {
		t.Fatalf("good document: %+v, %v", d, err)
	}
	for body, want := range map[string]string{
		`{`:                           "not valid JSON",
		`{"version":2,"future":true}`: "schema version 2",
		`{"version":1,"bogus":1}`:     "bogus",
		`{"version":1,"name":["x"]}`:  "state.json",
	} {
		err := Decode([]byte(body), "state.json", 1, &doc{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want it to mention %q", body, err, want)
		}
	}
}
