package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnOlderSchemaIsRefusedWithTheWayToStartOver(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"version": 2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lg, _ := newLog()
	_, err := Open(dir, lg)
	if err == nil {
		t.Fatal("opened a state of an older schema")
	}
	want := "state.json has schema version 2; this tnl knows version 3: it was written by an older tnl and can't be migrated before 1.0; back it up and start from an empty state directory (see docs/guide/upgrading.md)"
	if err.Error() != want {
		t.Fatalf("got %q\nwant %q", err, want)
	}
}

func TestANewerSchemaIsStillRefusedToUpgradeThisTnl(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"version": 99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lg, _ := newLog()
	_, err := Open(dir, lg)
	if err == nil || !strings.Contains(err.Error(), "upgrade this tnl") {
		t.Fatalf("got %v", err)
	}
}

func TestTheRefusalOverFilesWithoutStateNamesThem(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "caddy"), 0o700); err != nil {
		t.Fatal(err)
	}
	lg, _ := newLog()
	_, err := Open(dir, lg)
	if err == nil || !strings.Contains(err.Error(), "found caddy") {
		t.Fatalf("got %v", err)
	}
}
