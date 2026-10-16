package server_test

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
)

func TestBackupStreamsATarWithTheStateFile(t *testing.T) {
	f := start(t, "1.2.3", api.ActorLocal)
	if _, err := addZone(f.client, "example.com"); err != nil {
		t.Fatal(err)
	}
	body, _, err := f.client.Download(context.Background(), api.PathBackup)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	tr := tar.NewReader(body)
	h, err := tr.Next()
	if err != nil || h.Name != "state.json" {
		t.Fatalf("first entry %v, %v", h, err)
	}
	raw, _ := io.ReadAll(tr)
	if string(raw) != string(f.store.Raw()) {
		t.Fatalf("state.json in the archive differs from the store's")
	}
}

func TestBackupIsLocalOnly(t *testing.T) {
	f := start(t, "1.2.3", api.OutpostActor("nas"))
	_, _, err := f.client.Download(context.Background(), api.PathBackup)
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusForbidden {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(f.log.String(), `msg="request refused"`) {
		t.Errorf("a refused backup left no \"request refused\" line:\n%s", f.log.String())
	}
}
