// Package backup writes the archive `tnl backup` returns: state.json and
// Caddy's storage directory, without Caddy's lock and temp files, and without
// certificates whose key doesn't match.
package backup

import (
	"archive/tar"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// StateName and CaddyName are the archive's top-level entries.
const (
	StateName = "state.json"
	CaddyName = "caddy"
)

// Archive is what a backup will hold, decided before any byte is written, so
// the caller can report Skipped ahead of the stream.
type Archive struct {
	// Skipped names the certificates left out because their key doesn't match
	// (or can't be read), as the domain Caddy stored them under.
	Skipped []string

	caddyDir string
	entries  []entry
}

type entry struct {
	rel   string // slash path under the caddy directory
	isDir bool
}

// Prepare scans caddyDir. A missing directory is an archive without caddy/.
func Prepare(caddyDir string) (*Archive, error) {
	a := &Archive{caddyDir: caddyDir}
	err := filepath.WalkDir(caddyDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // vanished while walking
			}
			return err
		}
		relOS, _ := filepath.Rel(caddyDir, p)
		rel := filepath.ToSlash(relOS)
		if rel == "." {
			a.entries = append(a.entries, entry{rel: ".", isDir: true})
			return nil
		}
		if rel == "locks" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if isCertSite(rel) {
				if ok, named := certMatches(p); !ok {
					a.Skipped = append(a.Skipped, named)
					return filepath.SkipDir
				}
			}
			a.entries = append(a.entries, entry{rel: rel, isDir: true})
			return nil
		}
		if !d.Type().IsRegular() || isTempName(d.Name()) {
			return nil
		}
		a.entries = append(a.entries, entry{rel: rel})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(a.Skipped)
	return a, nil
}

// isCertSite reports whether rel is certificates/<issuer>/<domain>.
func isCertSite(rel string) bool {
	parts := strings.Split(rel, "/")
	return len(parts) == 3 && parts[0] == "certificates"
}

// isTempName reports whether a file name is one of certmagic's temp files,
// which are purely numeric random names beside the file they replace.
func isTempName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// certMatches reports whether the site directory's certificate and key belong
// together, and the name to report if not.
func certMatches(dir string) (bool, string) {
	name := filepath.Base(dir)
	crt, cerr := os.ReadFile(filepath.Join(dir, name+".crt"))
	key, kerr := os.ReadFile(filepath.Join(dir, name+".key"))
	if errors.Is(cerr, fs.ErrNotExist) && errors.Is(kerr, fs.ErrNotExist) {
		return true, name // nothing to compare
	}
	if cerr != nil || kerr != nil {
		return false, name
	}
	_, err := tls.X509KeyPair(crt, key)
	return err == nil, name
}

// WriteTo writes the tar archive: stateJSON as state.json, then caddy/.
// Files are archived with mode 0600 and directories 0700.
func (a *Archive) WriteTo(w io.Writer, stateJSON []byte) error {
	tw := tar.NewWriter(w)
	if err := tw.WriteHeader(&tar.Header{Name: StateName, Mode: 0o600, Size: int64(len(stateJSON)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write(stateJSON); err != nil {
		return err
	}
	for _, e := range a.entries {
		name := path.Join(CaddyName, e.rel)
		if e.isDir {
			if err := tw.WriteHeader(&tar.Header{Name: name + "/", Mode: 0o700, Typeflag: tar.TypeDir}); err != nil {
				return err
			}
			continue
		}
		// Caddy keeps writing: read the whole file so its size can't change
		// under the header, and tolerate one that has gone.
		b, err := os.ReadFile(filepath.Join(a.caddyDir, filepath.FromSlash(e.rel)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", e.rel, err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write(b); err != nil {
			return err
		}
	}
	return tw.Close()
}
