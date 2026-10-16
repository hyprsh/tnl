package server

import (
	"net/http"
	"path/filepath"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/backup"
)

// backup streams the archive: state.json copied under the store's lock, and
// the Caddy directory. Skipped certificates are named in a header, which is why
// the archive is planned before the first byte is sent.
func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(actorKey{}).(api.Actor)
	if err := localOnly(actor, "take a backup", "backup"); err != nil {
		s.reply(w, nil, s.logRefusal(r, actor, err))
		return
	}
	state := s.cfg.Store.Raw()
	a, err := backup.Prepare(filepath.Join(s.cfg.StateDir, backup.CaddyName))
	if err != nil {
		s.reply(w, nil, err)
		return
	}
	for _, name := range a.Skipped {
		w.Header().Add(api.SkippedHeader, name)
	}
	w.Header().Set("Content-Type", "application/x-tar")
	if err := a.WriteTo(w, state); err != nil {
		// Part of the archive is sent already: cut the connection so the
		// client sees a failure, not a short archive that looks whole.
		s.cfg.Log.Error("backup failed", "error", err)
		panic(http.ErrAbortHandler)
	}
	s.cfg.Log.Info("backup written", "actor", actor.String(), "skipped_certificates", len(a.Skipped))
}
