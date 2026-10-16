// Package edge assembles the Edge's runtime: the state store, WireGuard with
// one peer per Outpost, the API on tnl.sock and inside the Tunnel, Caddy (web.go) and the start-up
// log. Caddy's own code stays in edge/caddyx.
package edge

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
	"github.com/hyprsh/tnl/internal/edge/conntrack"
	"github.com/hyprsh/tnl/internal/edge/liveness"
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
	Build    string // "full" or "outpost"
	Log      *slog.Logger

	// WireGuardPort is the UDP port WireGuard listens on; 0 picks a free one.
	WireGuardPort int
	// Web starts Caddy beside the Tunnel; nil starts none.
	Web *Web
	// Started, if set, is called once the Edge is serving. The harness, and
	// the ticket that adds Caddy, use it to reach the running parts.
	Started func(*Running)
	// Resolver does the DNS checks that warn; nil uses the system's.
	Resolver server.Resolver
	// Revision is the VCS revision, logged and reported beside the build.
	Revision string
	// OfflineAfter is the silence after which an Outpost is Offline (60 s), and
	// LivenessPoll how often the Edge checks (1 s). The harness shrinks them.
	OfflineAfter, LivenessPoll time.Duration
}

// Running is what a started Edge exposes.
type Running struct {
	Store         *state.Store
	Tunnel        *tunnel.Tunnel
	WireGuardPort int // the port WireGuard actually listens on
	Liveness      *liveness.Liveness
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
	peers, err := syncPeers(tun, store, cfg.Log)
	if err != nil {
		return refuse(err)
	}
	ln, err := server.ListenSocket(cfg.StateDir)
	if err != nil {
		return refuse(err)
	}
	defer os.Remove(filepath.Join(cfg.StateDir, api.SocketName))
	conns := conntrack.New()
	hooks := livenessHooks{
		Version: cfg.Version, OfflineAfter: cfg.OfflineAfter, Poll: cfg.LivenessPoll, Log: cfg.Log,
		CloseOutpost: conns.CloseOutpost,
	}
	live := newLiveness(hooks, store.Snapshot())
	srv := server.New(server.Config{
		Conns: conns, Liveness: live,
		Store: store, Log: cfg.Log, Version: cfg.Version, Build: cfg.Build, Revision: cfg.Revision, Role: api.RoleEdge,
		Address: cfg.Address, WireGuardPort: wgPort, Resolver: cfg.Resolver, StateDir: cfg.StateDir,
		PreparePeer: peers.prepare,
		SwapPeer:    func(old, new keys.Key, a netip.Addr) { peers.swap(old, new, a, cfg.Log) },
		DropPeer:    func(k keys.Key, a netip.Addr) { peers.drop(k, a, cfg.Log) },
		RevokePeer:  func(old, new keys.Key, a netip.Addr) { peers.swap(old, new, a, cfg.Log) },
	})
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx, ln, api.ActorLocal) }()
	tunServed, err := serveTunnelAPI(ctx, srv, tun, store)
	if err != nil {
		return refuse(err)
	}

	if err := serveLiveness(ctx, live, hooks, tun, store); err != nil {
		return refuse(err)
	}

	if cfg.Web != nil {
		stopWeb, err := startWeb(cfg.Web, cfg.StateDir, wgPort, store, tun, conns, live, cfg.Log)
		if err != nil {
			return refuse(err)
		}
		defer stopWeb()
	}

	st := store.Snapshot()
	cfg.Log.Info("edge started",
		"version", cfg.Version,
		"build", cfg.Build,
		"revision", cfg.Revision,
		"address", cfg.Address,
		"key_fingerprint", st.PrivateKey.Public().Fingerprint(),
		"wireguard_port", wgPort,
		"state_dir", cfg.StateDir,
		"outposts", len(st.Outposts),
		"routes", len(st.Routes),
		"zones", len(st.Zones),
	)
	if cfg.Started != nil {
		cfg.Started(&Running{Store: store, Tunnel: tun, WireGuardPort: wgPort, Liveness: live})
	}
	select {
	case <-ctx.Done():
		<-served
		<-tunServed
		return nil
	case err := <-tunServed:
		cfg.Log.Error("the Tunnel API stopped", "error", err)
		return &logx.LoggedError{Err: err}
	case err := <-served:
		cfg.Log.Error("the API stopped", "error", err)
		return &logx.LoggedError{Err: err}
	}
}

// peerSet is the Edge's WireGuard peers, one per Outpost Tunnel address.
type peerSet struct {
	mu   sync.Mutex
	tun  *tunnel.Tunnel
	have map[netip.Addr]keys.Key
}

// syncPeers installs a WireGuard peer for every Outpost in the state, now and
// after every later change, so `outpost add` needs no restart. The Edge learns
// an Outpost's endpoint from its first packet, so a peer has none. An Outpost
// whose peer exists keeps it: the join exchange swaps the key itself, after
// its reply is out (peerSet.swap).
func syncPeers(tun *tunnel.Tunnel, store *state.Store, log *slog.Logger) (*peerSet, error) {
	ps := &peerSet{tun: tun, have: map[netip.Addr]keys.Key{}}
	if err := ps.apply(store.Snapshot()); err != nil {
		return nil, err
	}
	store.OnChange(func(st *state.State) {
		if err := ps.apply(st); err != nil {
			log.Error("could not update the WireGuard peers", "error", err)
		}
	})
	return ps, nil
}

func (ps *peerSet) apply(st *state.State) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, o := range st.Outposts {
		if _, ok := ps.have[o.Address]; ok {
			continue
		}
		if err := ps.tun.AddPeer(tunnel.Peer{PublicKey: o.PublicKey, AllowedIP: o.Address}); err != nil {
			return fmt.Errorf("add the WireGuard peer for outpost %q: %w", o.Name, err)
		}
		ps.have[o.Address] = o.PublicKey
	}
	return nil
}

// prepare adds a peer that owns no Tunnel address yet.
func (ps *peerSet) prepare(key keys.Key) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.tun.AddPeer(tunnel.Peer{PublicKey: key})
}

// swap replaces the peer at address: the new key takes the address over, then
// the old peer goes.
func (ps *peerSet) swap(old, new keys.Key, address netip.Addr, log *slog.Logger) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if err := ps.tun.AddPeer(tunnel.Peer{PublicKey: new, AllowedIP: address}); err != nil {
		log.Error("could not add the Outpost's new WireGuard peer", "address", address.String(), "error", err)
		return
	}
	if err := ps.tun.RemovePeer(old); err != nil {
		log.Error("could not remove the one-time WireGuard peer", "address", address.String(), "error", err)
	}
	ps.have[address] = new
}

// drop removes the peer at address, and forgets it.
func (ps *peerSet) drop(key keys.Key, address netip.Addr, log *slog.Logger) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if err := ps.tun.RemovePeer(key); err != nil {
		log.Error("could not remove the Outpost's WireGuard peer", "address", address.String(), "error", err)
	}
	delete(ps.have, address)
}
