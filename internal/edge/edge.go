// Package edge assembles the Edge's runtime. For now that is the state store
// the API on tnl.sock and the start-up log; the tunnel and Caddy join it in later tickets.
// Caddy itself stays in edge/caddyx.
package edge

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
	"github.com/hyprsh/tnl/internal/state"
)

// Config is what `tnl edge` passes in.
type Config struct {
	Address  string // --address: the DNS name Outposts dial
	StateDir string
	Version  string
	Build    string
	Log      *slog.Logger
}

// LoggedError wraps an error Run has already logged at error, so the caller
// only needs to exit.
type LoggedError struct{ Err error }

func (e *LoggedError) Error() string { return e.Err.Error() }
func (e *LoggedError) Unwrap() error { return e.Err }

// Run opens the state, logs the start-up line and serves until ctx ends. A
// state that fails its checks is logged at error and returned; nothing starts.
func Run(ctx context.Context, cfg Config) error {
	store, err := state.Open(cfg.StateDir, cfg.Log)
	if err != nil {
		cfg.Log.Error("refusing to start", "state_dir", cfg.StateDir, "error", err)
		return &LoggedError{err}
	}
	ln, err := server.ListenSocket(cfg.StateDir)
	if err != nil {
		cfg.Log.Error("refusing to start", "state_dir", cfg.StateDir, "error", err)
		return &LoggedError{err}
	}
	defer os.Remove(filepath.Join(cfg.StateDir, api.SocketName))
	srv := server.New(server.Config{Store: store, Log: cfg.Log, Version: cfg.Version, Build: cfg.Build, Role: "edge"})
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx, ln, api.ActorLocal) }()

	st := store.Snapshot()
	cfg.Log.Info("edge started",
		"version", cfg.Version,
		"build", cfg.Build,
		"address", cfg.Address,
		"key_fingerprint", st.PrivateKey.Public().Fingerprint(),
		"state_dir", cfg.StateDir,
		"outposts", len(st.Outposts),
		"routes", len(st.Routes),
		"zones", len(st.Zones),
	)
	select {
	case <-ctx.Done():
		<-served
		return nil
	case err := <-served:
		cfg.Log.Error("the API stopped", "error", err)
		return &LoggedError{err}
	}
}
