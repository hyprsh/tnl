package state

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/statefile"
)

// FileName is the state file inside the state directory.
const FileName = "state.json"

// ErrChangedOutside is returned when state.json no longer holds what the Edge
// last wrote.
var ErrChangedOutside = errors.New("state.json was changed outside tnl; restart the Edge to load it")

// DifferentKeyError is a state.json that holds another Edge key than the one
// the operator supplied. It names where the key came from and never shows it.
type DifferentKeyError struct{ Source string }

func (e *DifferentKeyError) Error() string {
	return "state.json holds a different Edge key than " + e.Source
}

// Store holds the state in memory and is its only writer.
type Store struct {
	dir string
	log *slog.Logger

	cur atomic.Pointer[State] // what readers see; never waits on mu

	mu   sync.Mutex // serialises changes
	last []byte     // exactly what the store last wrote (or loaded)
	subs []func(*State)
}

// Raw returns a copy of state.json as the store last wrote it, taken under the
// lock: never a file caught mid-change. It is what a backup holds.
func (s *Store) Raw() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.last)
}

// Open loads the state in dir, or creates a new Edge there when the directory
// is missing or empty. It refuses, naming the bad entry, a state that fails
// its checks, and never loads part of one.
func Open(dir string, log *slog.Logger) (*Store, error) {
	return OpenWithKey(dir, log, nil, "")
}

// OpenWithKey is Open for an Edge whose key the operator supplies. A new Edge
// takes key as its WireGuard key; an existing state must hold it, or the open is
// refused with "state.json holds a different Edge key than <source>", which
// names where the key came from and never shows the key. A nil key is Open.
func OpenWithKey(dir string, log *slog.Logger, key *keys.Key, source string) (*Store, error) {
	s := &Store{dir: dir, log: log}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("state directory: %w", err)
	}
	if fi, err := os.Stat(dir); err != nil {
		return nil, err
	} else if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, fmt.Errorf("state directory %s must be mode 0700: %w", dir, err)
		}
	}

	raw, err := os.ReadFile(s.path())
	switch {
	case err == nil:
		st, err := decode(raw)
		if err != nil {
			return nil, err
		}
		if key != nil && st.PrivateKey != *key {
			return nil, &DifferentKeyError{Source: source}
		}
		s.last = raw
		s.cur.Store(st)
	case errors.Is(err, os.ErrNotExist):
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		if len(entries) > 0 {
			return nil, fmt.Errorf("%s has files but no %s; refusing to create a new Edge over them", dir, FileName)
		}
		var priv, pub keys.Key
		if key != nil {
			priv, pub = *key, key.Public()
		} else if priv, pub, err = keys.Generate(); err != nil {
			return nil, err
		}
		st := &State{Version: Version, PrivateKey: priv}
		if err := s.save(st); err != nil {
			return nil, fmt.Errorf("create state: %w", err)
		}
		s.cur.Store(st)
		log.Info("no state in "+dir+", created a new Edge", "key_fingerprint", pub.Fingerprint())
	default:
		return nil, err
	}
	return s, nil
}

func (s *Store) path() string { return filepath.Join(s.dir, FileName) }

// Snapshot returns the current state. It never blocks. Callers must not
// modify it; a later change makes a new snapshot and leaves this one alone.
func (s *Store) Snapshot() *State { return s.cur.Load() }

// OnChange registers a publisher (the Route table, WireGuard peers). It runs
// under the lock right after the new snapshot is swapped in, so it must be
// quick; slow follow-ups belong after Update returns.
func (s *Store) OnChange(fn func(*State)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = append(s.subs, fn)
}

// Update applies one change. Under the lock it hands fn a private copy of the
// state, hands over any Hostname fn freed to the line waiting for it, checks the
// result, saves it, and swaps it in. If fn, the check or the save fails, nothing
// changes live. actor ("local" or "outpost:<name>") is for the logs.
func (s *Store) Update(actor api.Actor, fn func(*State) error) error {
	_, err := s.UpdateReport(actor, fn)
	return err
}

// UpdateReport is Update that also returns the waiting lines the change turned
// into Routes, so the command that freed a Hostname can say who serves it now.
// The handover is part of the change, not a step after it: no other change
// sees the Hostname free.
func (s *Store) UpdateReport(actor api.Actor, fn func(*State) error) ([]Handed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.cur.Load().Clone()
	if err := fn(next); err != nil {
		return nil, err
	}
	handed := next.Handover()
	if err := next.Validate(); err != nil {
		return nil, err
	}
	if err := s.save(next); err != nil {
		s.log.Warn("state change refused", "actor", actor.String(), "error", err)
		return nil, err
	}
	s.cur.Store(next)
	for _, h := range handed {
		s.log.Info("declared line applies", "actor", actor.String(), "outpost", h.Outpost, "hostname", h.Route.Hostname, "service", h.Route.Service)
	}
	for _, sub := range s.subs {
		sub(next)
	}
	return handed, nil
}

// save writes st to state.json: temp file, fsync, rename, fsync the directory.
// It first checks that the file still holds what the store last wrote. The
// caller holds mu (or is Open).
func (s *Store) save(st *State) error {
	if s.last != nil {
		cur, err := os.ReadFile(s.path())
		if err != nil || !bytes.Equal(cur, s.last) {
			return ErrChangedOutside
		}
	}
	data, err := encode(st)
	if err != nil {
		return err
	}
	err = statefile.Write(s.path(), data)
	switch {
	case err == nil:
	case errors.Is(err, statefile.ErrDirSync):
		// The rename happened, so the file is what we meant to write; the
		// change is still reported as failed, and the next save sees this
		// content as the last one.
	default:
		return err
	}
	s.last = data
	return err
}
