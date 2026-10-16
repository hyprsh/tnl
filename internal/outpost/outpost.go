// Package outpost is the Outpost's runtime behind `tnl connect`: it reads the
// join token or the saved state, brings the Tunnel up and serves the
// forwarder. It runs as a plain user and imports nothing from the Edge or
// Caddy, so the Outpost-only build links it.
//
// In slice 1 the Outpost joins by keeping the token's key. Slice 3 swaps it
// inside the Tunnel.
package outpost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/forwarder"
	"github.com/hyprsh/tnl/internal/logx"
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
	// Started, if set, is called once the Tunnel is up and the forwarder listens.
	Started func(*Running)
	// Revision is the VCS revision, logged beside the build.
	Revision string
}

// Running is what a started Outpost exposes.
type Running struct {
	Tunnel  *tunnel.Tunnel
	Address netip.Addr // the Outpost's Tunnel address
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

	joining := false
	switch {
	case st == nil && tok == nil:
		return configErr("no state in %s and no join token: pass --token (TNL_TOKEN) from `tnl outpost add` on the Edge host", cfg.StateDir)
	case st == nil:
		joining = true
		st = &State{
			PrivateKey: tok.OneTimeKey, Address: tok.TunnelAddr,
			EdgeAddress: tok.Address, EdgePort: tok.Port, EdgeKey: tok.EdgeKey,
			TokenSHA256: token.Hash(cfg.Token),
		}
	case tok != nil && token.Hash(cfg.Token) == st.TokenSHA256:
		cfg.Log.Info("ignoring the join token: this state was made from it", "state_dir", cfg.StateDir)
	case tok != nil:
		return configErr("%s already holds an Outpost made from another token; joining again with a different token is not supported yet", cfg.StateDir)
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
	ln, err := tun.Net.ListenTCPAddrPort(netip.AddrPortFrom(st.Address, wire.DataPort))
	if err != nil {
		return fmt.Errorf("listen on the Tunnel address: %w", err)
	}
	fw := &forwarder.Forwarder{Version: cfg.Version}
	served := make(chan struct{})
	go func() { defer close(served); fw.Serve(ln) }()
	defer func() { ln.Close(); <-served }()

	if joining {
		if err := saveState(cfg.StateDir, st); err != nil {
			return fmt.Errorf("save the state: %w", err)
		}
	}
	cfg.Log.Info("outpost started",
		"version", cfg.Version,
		"build", cfg.Build,
		"revision", cfg.Revision,
		"tunnel_address", st.Address.String(),
		"edge_address", st.EdgeAddress,
		"edge_ip", edgeIP.String(),
		"edge_port", st.EdgePort,
		"state_dir", cfg.StateDir,
		"joined", joining,
	)
	if cfg.Started != nil {
		cfg.Started(&Running{Tunnel: tun, Address: st.Address, API: &api.Client{
			Version: cfg.Version,
			Dial: func(ctx context.Context, _, address string) (net.Conn, error) {
				ap, err := netip.ParseAddrPort(address)
				if err != nil {
					return nil, err
				}
				return tun.Net.DialContextTCPAddrPort(ctx, ap)
			},
		}})
	}
	<-ctx.Done()
	return nil
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
