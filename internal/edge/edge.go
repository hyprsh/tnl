// Package edge assembles the Edge's runtime: the state store, WireGuard with
// one peer per Outpost, the API on tnl.sock and inside the Tunnel, Caddy (web.go) and the start-up
// log. Caddy's own code stays in edge/caddyx.
package edge

import (
	"context"
	"errors"
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
	"github.com/hyprsh/tnl/internal/edge/caddyx"
	"github.com/hyprsh/tnl/internal/edge/conntrack"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/remind"
	"github.com/hyprsh/tnl/internal/state"
	"github.com/hyprsh/tnl/internal/tunnel"
	"github.com/hyprsh/tnl/internal/udpbuf"
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
	// LivenessPoll how often the Edge checks (1 s). SilentAfter is the silence
	// after which the Edge closes an Outpost's Tunnel connections without
	// calling it Offline (20 s). The harness shrinks them.
	OfflineAfter, SilentAfter, LivenessPoll time.Duration
	// UDPBuffers measures the UDP buffer a socket is granted, for the warn at
	// start; nil measures a real socket. Tests set it.
	UDPBuffers udpbuf.Measure
	// LogLevel is the level the logger runs at, for `status` to report.
	LogLevel string
	// CertWait bounds the certificate wait at `route add`; zero is 60 s.
	CertWait time.Duration
	// DNSResolvers are the DNS servers ("host:port") the SOA lookup and the
	// DNS-01 challenges ask; none means the system's. The harness points them
	// at its own server.
	DNSResolvers []string
	// Reminders and Clock drive the reminders for waiting lines; the zero
	// Schedule is remind.Standard and a nil Clock is the wall clock.
	Reminders remind.Schedule
	Clock     remind.Clock

	// Key is the Edge's WireGuard private key from --key-file, TNL_KEY_FILE or
	// TNL_KEY, and KeySource names which, for the refusal when state.json holds
	// another. Nil generates a key at first start.
	Key       *keys.Key
	KeySource string
	// Zones and Outposts are the declared lines, already checked. A kind with
	// lines is the Edge's whole set of that kind; a kind with none is the CLI's.
	Zones    []state.DeclaredZone
	Outposts []state.DeclaredOutpost
}

// ConfigError is a problem the operator must fix in the Edge's settings; `tnl
// edge` exits 78.
type ConfigError struct{ Err error }

func (e *ConfigError) Error() string { return e.Err.Error() }
func (e *ConfigError) Unwrap() error { return e.Err }

// Running is what a started Edge exposes.
type Running struct {
	Store         *state.Store
	Tunnel        *tunnel.Tunnel
	WireGuardPort int // the port WireGuard actually listens on
	Liveness      *liveness.Liveness
	// Certs is the certificate manager; nil when the Edge has no Caddy.
	Certs *caddyx.Certs
}

// Run opens the state, brings WireGuard up with every Outpost in it as a peer,
// logs the start-up line and serves until ctx ends. A state that fails its
// checks is logged at error and returned; nothing starts.
func Run(ctx context.Context, cfg Config) error {
	refuse := func(err error) error {
		cfg.Log.Error("refusing to start", "state_dir", cfg.StateDir, "error", err)
		return &logx.LoggedError{Err: err}
	}
	store, err := state.OpenWithKey(cfg.StateDir, cfg.Log, cfg.Key, cfg.KeySource)
	if err != nil {
		var diff *state.DifferentKeyError
		if errors.As(err, &diff) {
			err = &ConfigError{err}
		}
		return refuse(err)
	}
	checks, err := applyDeclared(ctx, store, cfg)
	if err != nil {
		return refuse(&ConfigError{err})
	}
	if err := udpbuf.SilenceQuicGo(); err != nil {
		return refuse(err)
	}
	udpbuf.Warn(cfg.Log, cfg.UDPBuffers)
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
	ln, err := api.ListenSocket(cfg.StateDir)
	if err != nil {
		return refuse(err)
	}
	defer os.Remove(filepath.Join(cfg.StateDir, api.SocketName))
	if err := checkProviders(store.Snapshot()); err != nil {
		return refuse(err)
	}
	sched := cfg.Reminders
	if sched.Every == 0 && len(sched.After) == 0 {
		sched = remind.Standard
	}
	// Whatever Run starts that writes the state ends before Run returns, so a
	// restart on the same state directory never meets a writer of this run.
	ctx, stopBackground := context.WithCancel(ctx)
	var bg sync.WaitGroup
	defer bg.Wait()
	defer stopBackground()
	waits := newWaitingWatch(cfg.Log, sched, cfg.Clock)
	waits.sync(store.Snapshot())
	store.OnChange(waits.sync)
	tickEvery := time.Second
	if sched.Every > 0 && sched.Every < 4*time.Second {
		tickEvery = sched.Every / 4
	}
	bg.Go(func() { waits.run(ctx, store, tickEvery) })
	conns := conntrack.New()
	certs := new(certsRef)
	wild := &wildcards{resolvers: cfg.DNSResolvers, certs: certs}
	hooks := livenessHooks{
		Version: cfg.Version, OfflineAfter: cfg.OfflineAfter, Poll: cfg.LivenessPoll, Log: cfg.Log,
		SilentAfter: cfg.SilentAfter, CloseOutpost: conns.CloseOutpost, CloseSilent: conns.CloseOutpost,
	}
	live := newLiveness(hooks, store.Snapshot())
	testDial := new(lateTestDialer)
	srv := server.New(server.Config{
		Conns: conns, Liveness: live, Certs: certs, CertReader: certs, TestDialer: testDial, Wildcards: wild,
		Store: store, Log: cfg.Log, Version: cfg.Version, Build: cfg.Build, Revision: cfg.Revision, Role: api.RoleEdge,
		Address: cfg.Address, WireGuardPort: wgPort, Resolver: cfg.Resolver, StateDir: cfg.StateDir, CertWait: cfg.CertWait,
		StartedAt: time.Now(), LogLevel: cfg.LogLevel, AccessLog: cfg.Web != nil && cfg.Web.AccessLog,
		ZonesDeclared: len(cfg.Zones) > 0, OutpostsDeclared: len(cfg.Outposts) > 0,
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

	if err := serveLiveness(ctx, &bg, live, hooks, tun, store); err != nil {
		return refuse(err)
	}

	if cfg.Web != nil {
		stopWeb, err := startWeb(cfg.Web, webDeps{StateDir: cfg.StateDir, WGPort: wgPort, Store: store, Tunnel: tun, Conns: conns, Live: live, Certs: certs, TestDial: testDial, Wild: wild, DNSResolvers: cfg.DNSResolvers, Log: cfg.Log})
		if err != nil {
			return refuse(err)
		}
		defer stopWeb()
	}

	// The remote checks of declared Zones never stop the Edge.
	var checked sync.WaitGroup
	if len(checks) > 0 {
		checked.Add(1)
		go func() { defer checked.Done(); srv.CheckDeclaredZones(ctx, checks, sched, cfg.Clock) }()
	}
	defer checked.Wait()

	st := store.Snapshot()
	declaredZones, declaredOutposts := declaredCounts(st)
	cfg.Log.Info("edge started",
		"version", cfg.Version,
		"build", cfg.Build,
		"revision", cfg.Revision,
		"address", cfg.Address,
		"key_fingerprint", st.PrivateKey.Public().Fingerprint(),
		"wireguard_port", wgPort,
		"state_dir", cfg.StateDir,
		"outposts", len(st.Outposts),
		"outposts_declared", declaredOutposts,
		"routes", len(st.Routes),
		"zones", len(st.Zones),
		"zones_declared", declaredZones,
	)
	if cfg.Started != nil {
		cfg.Started(&Running{Store: store, Tunnel: tun, WireGuardPort: wgPort, Liveness: live, Certs: certs.manager()})
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

// applyDeclared brings the Zones and Outposts to the Edge's settings in one
// locked change, before the peers and Caddy's config are built from the state,
// and logs what it did. It returns the Zones whose remote checks run later.
func applyDeclared(ctx context.Context, store *state.Store, cfg Config) ([]state.Zone, error) {
	if len(cfg.Zones) == 0 && len(cfg.Outposts) == 0 && !anyDeclared(store.Snapshot()) {
		return nil, nil
	}
	var res state.DeclaredResult
	_, err := store.UpdateReport(api.ActorLocal, func(st *state.State) (err error) {
		res, err = st.ApplyDeclared(cfg.Zones, cfg.Outposts)
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, e := range res.Events {
		cfg.Log.Log(ctx, e.Level, e.Msg, append([]any{"actor", api.ActorLocal.String()}, e.Attrs...)...)
	}
	return res.Checks, nil
}

// declaredCounts is how many of the state's Zones and Outposts are declared.
func declaredCounts(st *state.State) (zones, outposts int) {
	for _, z := range st.Zones {
		if z.Declared {
			zones++
		}
	}
	for _, o := range st.Outposts {
		if o.Declared {
			outposts++
		}
	}
	return zones, outposts
}

func anyDeclared(st *state.State) bool {
	zones, outposts := declaredCounts(st)
	return zones+outposts > 0
}

// peerSet is the Edge's WireGuard peers, one per Outpost Tunnel address.
type peerSet struct {
	mu   sync.Mutex
	tun  *tunnel.Tunnel
	have map[netip.Addr]keys.Key
}

// syncPeers installs a WireGuard peer for every Outpost in the state, now and
// after every later change, so `outpost add` needs no restart. The Edge learns
// an Outpost's endpoint from its first packet, unless the state remembers one. An Outpost
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
		if err := ps.tun.AddPeer(tunnel.Peer{PublicKey: o.PublicKey, AllowedIP: o.Address, Endpoint: o.Endpoint}); err != nil {
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
