// Package statefile is what the Edge's and the Outpost's state.json share: the
// write that never leaves half a file, and the read that checks the schema
// version before anything else.
package statefile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrDirSync wraps the error of a failed fsync of the directory after the
// rename. The file then holds the new content, but the rename may not survive a
// crash.
var ErrDirSync = errors.New("could not sync the directory")

// tempFile is what Write needs of the temp file; tests swap createTemp to fail it.
type tempFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

var createTemp = func(name string) (tempFile, error) {
	return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
}

// Write replaces path with data (mode 0600): temp file, fsync, rename, fsync the
// directory. A crash leaves the old file or the new one, never part of one.
func Write(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := createTemp(tmp)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDirSync, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("%w: %w", ErrDirSync, err)
	}
	return nil
}

// Decode reads raw into v, which must have a `version` field. It checks the
// version first, so a file from another schema is refused as that and not for
// its fields, and then refuses fields v does not know. name is the file's name
// for the errors.
func Decode(raw []byte, name string, version int, v any) error {
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", name, err)
	}
	if head.Version != version {
		return &SchemaError{Name: name, Have: head.Version, Known: version}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
