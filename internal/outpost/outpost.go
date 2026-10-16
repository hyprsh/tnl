// Package outpost is the Outpost's runtime behind `tnl connect`: it reads the
// join token or the saved state, brings the Tunnel up and serves the
// forwarder. It runs as a plain user and imports nothing from the Edge or
// Caddy, so the Outpost-only build links it.
//
// An Outpost joins by swapping the token's one-time key for its own inside the
// Tunnel (ADR 0003): the token is worthless once used.
package outpost

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/allow"
	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/forwarder"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/remind"
	"github.com/hyprsh/tnl/internal/token"
	"github.com/hyprsh/tnl/internal/tunnel"
	"github.com/hyprsh/tnl/internal/wire"
)

// Config is what `tnl connect` passes in.
type Config struct {
	Token    string // empty to reconnect from the state
	StateDir string
	Version  string
	Build    string // "full" or "outpost"
	Log      *slog.Logger

	// Resolve turns the Edge's address into an IP, because wireguard-go takes
	// only IPs. Nil uses the system resolver.
	Resolve func(ctx context.Context, host string) (netip.Addr, error)
	// JoinTimeout bounds the join exchange; zero is 30 s. A used or revoked
	// token never gets an answer, so the join gives up where a joined Outpost
	// never does.
	JoinTimeout time.Duration
	// Started, if set, is called once the Tunnel is up and the forwarder listens.
	Started func(*Running)
	// Revision is the VCS revision, logged beside the build.
	Revision string
	// PingInterval is how often the Outpost pings the Edge; 0 is 10 s. The
	// harness shrinks it.
	PingInterval time.Duration
	// RelayDialTimeout bounds each attempt of the relay to reach the Edge's
	// API; zero is 5 s.
	RelayDialTimeout time.Duration
	// NoAnswerAfter is the silence after which the Edge is said not to answer and
	// its address is resolved again, and the interval after that; zero is 30 s.
	NoAnswerAfter time.Duration
	// Reminders is the schedule of the "no answer" reminders; the zero value is
	// remind.Standard. Clock is the watcher's clock; nil is the wall clock.
	Reminders remind.Schedule
	Clock     remind.Clock
	// Allow limits the Services the Edge may name; nil allows every one.
	Allow *allow.List
}

// Running is what a started Outpost exposes.
type Running struct {
	Tunnel  *tunnel.Tunnel
	Address netip.Addr // the Outpost's Tunnel address
	Name    string     // the Outpost's name
	// API calls the Edge's API through the Tunnel, as this Outpost.
	API *api.Client
}

// ConfigError is a problem the operator must fix; `tnl connect` exits 78.
type ConfigError struct{ Err error }

func (e *ConfigError) Error() string { return e.Err.Error() }
func (e *ConfigError) Unwrap() error { return e.Err }

func configErr(format string, a ...any) error { return &ConfigError{fmt.Errorf(format, a...)} }

// Run joins or reconnects, then serves until ctx ends. A token this tnl can't
// read is refused before the state directory is touched.
func Run(ctx context.Context, cfg Config) error {
	var tok *token.Token
	if cfg.Token != "" {
		t, err := token.Decode(cfg.Token)
		if err != nil {
			return &ConfigError{err}
		}
		tok = &t
	}
	st, err := loadState(cfg.StateDir)
	if err != nil {
		return configErr("state in %s: %w", cfg.StateDir, err)
	}

	// A state made from the token that is passed again is kept; a different
	// token joins again and replaces the state.
	joining := false
	switch {
	case st == nil && tok == nil:
		return configErr("no state in %s and no join token: pass --token (TNL_TOKEN) from `tnl outpost add` on the Edge host", cfg.StateDir)
	case st != nil && tok != nil && token.Hash(cfg.Token) == st.TokenSHA256:
		cfg.Log.Info("ignoring the join token: this state was made from it", "state_dir", cfg.StateDir)
	case tok != nil:
		joining = true
		st = &State{
			PrivateKey: tok.OneTimeKey, Address: tok.TunnelAddr,
			EdgeAddress: tok.Address, EdgePort: tok.Port, EdgeKey: tok.EdgeKey,
			TokenSHA256: token.Hash(cfg.Token),
		}
	}

	edgeIP, err := resolve(ctx, cfg, st.EdgeAddress)
	if err != nil {
		return fmt.Errorf("resolve the Edge's address %s: %w", st.EdgeAddress, err)
	}
	tun, err := tunnel.New(tunnel.Config{Address: st.Address, PrivateKey: st.PrivateKey, Logger: logx.WireGuardLogger(cfg.Log)})
	if err != nil {
		return err
	}
	defer tun.Close()
	err = tun.AddPeer(tunnel.Peer{
		PublicKey:        st.EdgeKey,
		AllowedIP:        addr.Edge,
		Endpoint:         netip.AddrPortFrom(edgeIP, uint16(st.EdgePort)),
		KeepaliveSeconds: tunnel.OutpostKeepalive,
	})
	if err != nil {
		return err
	}
	edgeAPI := &api.Client{
		Version: cfg.Version,
		Dial: func(ctx context.Context, _, address string) (net.Conn, error) {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return nil, err
			}
			return tun.Net.DialContextTCPAddrPort(ctx, ap)
		},
	}

	var edgeVersion string
	if joining {
		edgeVersion, err = joinExchange(ctx, cfg, edgeAPI, tun, st)
		if err != nil {
			return err
		}
	}

	ln, err := tun.Net.ListenTCPAddrPort(netip.AddrPortFrom(st.Address, wire.DataPort))
	if err != nil {
		return fmt.Errorf("listen on the Tunnel address: %w", err)
	}
	fw := &forwarder.Forwarder{Version: cfg.Version, Allow: cfg.Allow, Log: cfg.Log}
	served := make(chan struct{})
	go func() { defer close(served); fw.Serve(ln) }()
	defer func() { ln.Close(); <-served }()

	pingCtx, stopPinging := context.WithCancel(ctx)
	defer stopPinging()
	w := newWatcher(cfg, tun, st, edgeIP, func(ctx context.Context, host string) (netip.Addr, error) { return resolve(ctx, cfg, host) })
	go w.run(pingCtx)
	if err := runPinger(pingCtx, tun, st.Address, cfg.Version, cmp.Or(cfg.PingInterval, DefaultPingInterval), w.answered, cfg.Log); err != nil {
		return fmt.Errorf("start pinging the Edge: %w", err)
	}
	stopRelay, err := startRelay(cfg, tun)
	if err != nil {
		return err
	}
	defer stopRelay()

	fields := []any{
		"name", st.Name,
		"version", cfg.Version,
		"build", cfg.Build,
		"revision", cfg.Revision,
		"tunnel_address", st.Address.String(),
		"edge_address", st.EdgeAddress,
		"edge_ip", edgeIP.String(),
		"edge_port", st.EdgePort,
		"state_dir", cfg.StateDir,
		"joined", joining,
		"allow", cfg.Allow.String(),
	}
	if joining {
		fields = append(fields, "edge_version", edgeVersion)
	}
	cfg.Log.Info("outpost started", fields...)
	if cfg.Started != nil {
		cfg.Started(&Running{Tunnel: tun, Address: st.Address, Name: st.Name, API: edgeAPI})
	}
	<-ctx.Done()
	return nil
}

// joinExchange is the Outpost's half of the join: it asks the Edge to swap the
// one-time key for a new key pair's public half, writes the state (name
// included) and switches to the new key. It leaves st holding the new key and
// the name. Nothing is written unless the Edge accepted.
func joinExchange(ctx context.Context, cfg Config, edgeAPI *api.Client, tun *tunnel.Tunnel, st *State) (edgeVersion string, err error) {
	priv, pub, err := keys.Generate()
	if err != nil {
		return "", err
	}
	timeout := cfg.JoinTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	jctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := api.Call[api.JoinReply](jctx, edgeAPI, http.MethodPost, api.PathJoin, api.JoinRequest{PublicKey: pub, Version: cfg.Version})
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) {
			return "", &ConfigError{fmt.Errorf("the Edge refused the join: %s", ae.Message)}
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("could not join through the Tunnel (waited up to %s; is the token already used or revoked, or the Edge unreachable?): %w", timeout, err)
	}
	st.Name, st.PrivateKey = resp.Name, priv
	if err := saveState(cfg.StateDir, st); err != nil {
		return "", fmt.Errorf("save the state: %w", err)
	}
	if err := tun.SetPrivateKey(priv); err != nil {
		return "", fmt.Errorf("switch to the Outpost's own key: %w", err)
	}
	// The Edge learns the new key's endpoint from a first packet, so start one.
	pctx, pcancel := context.WithTimeout(ctx, timeout)
	defer pcancel()
	if _, err := api.Call[api.Info](pctx, edgeAPI, http.MethodGet, api.PathInfo, nil); err != nil {
		cfg.Log.Warn("the Edge did not answer after the key swap yet", "error", err)
	}
	return resp.EdgeVersion, nil
}

// resolve returns the Edge's IP: the address itself if it is one, else the
// first answer, IPv4 first.
func resolve(ctx context.Context, cfg Config, host string) (netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip, nil
	}
	if cfg.Resolve != nil {
		return cfg.Resolve(ctx, host)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return netip.Addr{}, err
	}
	for _, ip := range ips {
		if ip.Unmap().Is4() {
			return ip.Unmap(), nil
		}
	}
	if len(ips) == 0 {
		return netip.Addr{}, errors.New("no addresses")
	}
	return ips[0], nil
}

// startRelay serves the Outpost's tnl.sock in its state directory and returns
// the function that stops it.
func startRelay(cfg Config, tun *tunnel.Tunnel) (stop func(), err error) {
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("state directory: %w", err)
	}
	ln, err := api.ListenSocket(cfg.StateDir)
	if err != nil {
		return nil, fmt.Errorf("serve the relay socket: %w", err)
	}
	timeout := cfg.RelayDialTimeout
	if timeout == 0 {
		timeout = defaultRelayDialTimeout
	}
	r := &relay{
		info: api.Info{Role: api.RoleOutpost, Version: cfg.Version, Build: cfg.Build, Revision: cfg.Revision},
		log:  cfg.Log,
		dial: func(ctx context.Context, address string) (net.Conn, error) {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return nil, err
			}
			return tun.Net.DialContextTCPAddrPort(ctx, ap)
		},
		timeout: timeout,
	}
	hs := &http.Server{Handler: r.handler(), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() { defer close(done); hs.Serve(ln) }()
	return func() { hs.Close(); <-done }, nil
}
