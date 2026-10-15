// Package edge assembles the Edge's runtime: the state store, WireGuard with
// one peer per Outpost, the API on tnl.sock, Caddy (web.go) and the start-up
// log. Caddy's own code stays in edge/caddyx.
package edge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/state"
	"github.com/hyprsh/tnl/internal/tunnel"
)

// Config is what `tnl edge` passes in.
type Config struct {
	Address  string // --address: the DNS name Outposts dial
	StateDir string
	Version  string
	Build    string
	Log      *slog.Logger

	// WireGuardPort is the UDP port WireGuard listens on; 0 picks a free one.
	WireGuardPort int
	// Web starts Caddy beside the Tunnel; nil starts none.
	Web *Web
	// Started, if set, is called once the Edge is serving. The harness, and
	// the ticket that adds Caddy, use it to reach the running parts.
	Started func(*Running)
}

// Running is what a started Edge exposes.
type Running struct {
	Store         *state.Store
	Tunnel        *tunnel.Tunnel
	WireGuardPort int // the port WireGuard actually listens on
}

// Run opens the state, brings WireGuard up with every Outpost in it as a peer,
// logs the start-up line and serves until ctx ends. A state that fails its
// checks is logged at error and returned; nothing starts.
func Run(ctx context.Context, cfg Config) error {
	refuse := func(err error) error {
		cfg.Log.Error("refusing to start", "state_dir", cfg.StateDir, "error", err)
		return &logx.LoggedError{Err: err}
	}
	store, err := state.Open(cfg.StateDir, cfg.Log)
	if err != nil {
		return refuse(err)
	}
	tun, err := tunnel.New(tunnel.Config{
		Address:    addr.Edge,
		PrivateKey: store.Snapshot().PrivateKey,
		ListenPort: cfg.WireGuardPort,
		Logger:     logx.WireGuardLogger(cfg.Log),
	})
	if err != nil {
		return refuse(err)
	}
	defer tun.Close()
	wgPort, err := tun.ListenPort()
	if err != nil {
		return refuse(err)
	}
	if err := syncPeers(tun, store, cfg.Log); err != nil {
		return refuse(err)
	}
	ln, err := server.ListenSocket(cfg.StateDir)
	if err != nil {
		return refuse(err)
	}
	defer os.Remove(filepath.Join(cfg.StateDir, api.SocketName))
	srv := server.New(server.Config{
		Store: store, Log: cfg.Log, Version: cfg.Version, Build: cfg.Build, Role: api.RoleEdge,
		Address: cfg.Address, WireGuardPort: wgPort,
	})
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx, ln, api.ActorLocal) }()

	if cfg.Web != nil {
		stopWeb, err := startWeb(cfg.Web, cfg.StateDir, wgPort, store, tun, cfg.Log)
		if err != nil {
			return refuse(err)
		}
		defer stopWeb()
	}

	st := store.Snapshot()
	cfg.Log.Info("edge started",
		"version", cfg.Version,
		"build", cfg.Build,
		"address", cfg.Address,
		"key_fingerprint", st.PrivateKey.Public().Fingerprint(),
		"wireguard_port", wgPort,
		"state_dir", cfg.StateDir,
		"outposts", len(st.Outposts),
		"routes", len(st.Routes),
		"zones", len(st.Zones),
	)
	if cfg.Started != nil {
		cfg.Started(&Running{Store: store, Tunnel: tun, WireGuardPort: wgPort})
	}
	select {
	case <-ctx.Done():
		<-served
		return nil
	case err := <-served:
		cfg.Log.Error("the API stopped", "error", err)
		return &logx.LoggedError{Err: err}
	}
}

// syncPeers installs a WireGuard peer for every Outpost in the state, now and
// after every later change, so `outpost add` needs no restart. The Edge learns
// an Outpost's endpoint from its first packet, so a peer has none.
func syncPeers(tun *tunnel.Tunnel, store *state.Store, log *slog.Logger) error {
	have := map[keys.Key]bool{}
	apply := func(st *state.State) error {
		for _, o := range st.Outposts {
			if have[o.PublicKey] {
				continue
			}
			if err := tun.AddPeer(tunnel.Peer{PublicKey: o.PublicKey, AllowedIP: o.Address}); err != nil {
				return fmt.Errorf("add the WireGuard peer for outpost %q: %w", o.Name, err)
			}
			have[o.PublicKey] = true
		}
		return nil
	}
	if err := apply(store.Snapshot()); err != nil {
		return err
	}
	store.OnChange(func(st *state.State) {
		if err := apply(st); err != nil {
			log.Error("could not update the WireGuard peers", "error", err)
		}
	})
	return nil
}
