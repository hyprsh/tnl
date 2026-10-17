//go:build !outpost

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hyprsh/tnl/internal/api"
)

func init() {
	commands["backup"] = backupCommand
}

// backupCommand asks the running Edge for its archive and writes it to the
// file, or to stdout for "-". A file is written beside its final name and
// renamed into place, so a failed backup never leaves a partial archive that
// looks like a good one. It is created 0600, and replaces a file of that name.
func backupCommand(args []string) error {
	c := newClientCommand("backup")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("backup", "usage: tnl backup <file|->")
	}
	body, hdr, err := c.client().Download(context.Background(), api.PathBackup)
	if err != nil {
		return err
	}
	defer body.Close()

	var w io.Writer = stdout
	var tmp *os.File
	if pos[0] != "-" {
		tmp, err = os.CreateTemp(filepath.Dir(pos[0]), "."+filepath.Base(pos[0])+".*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name()) // a no-op once renamed
		if err := tmp.Chmod(0o600); err != nil {
			return err
		}
		w = tmp
	}
	if _, err := io.Copy(w, body); err != nil {
		if tmp != nil {
			tmp.Close()
		}
		return fmt.Errorf("the backup stopped before it was complete: %w", err)
	}
	if tmp != nil {
		if err := tmp.Sync(); err != nil {
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), pos[0]); err != nil {
			return err
		}
	}
	for _, name := range hdr.Values(api.SkippedHeader) {
		fmt.Fprintf(stderr, "left out the certificate for %s: its key doesn't match; Caddy obtains it again after a restore\n", name)
	}
	return nil
}
