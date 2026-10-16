package statefile

import (
	"errors"
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

// failingFile is a temp file whose Write or Sync fails.
type failingFile struct {
	writeErr, syncErr error
}

func (f failingFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}
func (f failingFile) Sync() error  { return f.syncErr }
func (f failingFile) Close() error { return nil }

func TestWriteFailsAndKeepsTheOldFileWhenWriteOrSyncFails(t *testing.T) {
	for name, f := range map[string]failingFile{
		"write": {writeErr: errors.New("disk full")},
		"sync":  {syncErr: errors.New("I/O error")},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := Write(path, []byte("old")); err != nil {
				t.Fatal(err)
			}
			create := createTemp
			t.Cleanup(func() { createTemp = create })
			createTemp = func(name string) (tempFile, error) {
				if err := os.WriteFile(name, nil, 0o600); err != nil {
					return nil, err
				}
				return f, nil
			}

			if err := Write(path, []byte("new")); err == nil {
				t.Fatal("Write succeeded though the temp file's " + name + " failed")
			}
			if got, _ := os.ReadFile(path); string(got) != "old" {
				t.Errorf("state.json holds %q, want the old content kept", got)
			}
			if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
				t.Errorf("temp file left behind: %v", err)
			}
		})
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
