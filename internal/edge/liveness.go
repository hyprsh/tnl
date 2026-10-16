package edge

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/state"
	"github.com/hyprsh/tnl/internal/tunnel"
	"github.com/hyprsh/tnl/internal/wire"
)

// defaultLivenessPoll is how often the Edge looks at WireGuard for traffic from
// the Outposts and for the Offline window running out.
const defaultLivenessPoll = time.Second

// livenessHooks is what startLiveness needs from the rest of the Edge.
type livenessHooks struct {
	Version      string
	OfflineAfter time.Duration // 0 is 60 s
	Poll         time.Duration // 0 is 1 s
	Log          *slog.Logger
	// CloseOutpost closes the Outpost's Tunnel connections when it goes Offline.
	CloseOutpost func(name string)
}

// newLiveness makes the Edge's Liveness and counts its own start as hearing
// from every Outpost the state holds.
func newLiveness(h livenessHooks, st *state.State) *liveness.Liveness {
	l := liveness.New(liveness.Config{OfflineAfter: h.OfflineAfter, Log: h.Log, OnOffline: h.CloseOutpost})
	for _, o := range st.Outposts {
		if o.OneTimeKey == nil {
			l.Seed(o.Name)
		}
	}
	return l
}

// serveLiveness listens for pings on 100.64.0.1:wire.PingPort inside the Tunnel,
// and watches WireGuard for any other traffic from an Outpost and for its
// endpoint. Both stop when ctx ends.
func serveLiveness(ctx context.Context, l *liveness.Liveness, h livenessHooks, tun *tunnel.Tunnel, store *state.Store) error {
	conn, err := tun.Net.ListenUDPAddrPort(netip.AddrPortFrom(addr.Edge, wire.PingPort))
	if err != nil {
		return fmt.Errorf("listen on the Tunnel for pings: %w", err)
	}
	outpostAt := func(a netip.Addr) (string, bool) {
		for _, o := range store.Snapshot().Outposts {
			if o.Address == a {
				return o.Name, true
			}
		}
		return "", false
	}
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			ap, err := netip.ParseAddrPort(from.String())
			if err != nil {
				continue
			}
			name, ok := outpostAt(ap.Addr())
			if !ok {
				continue // not an Outpost: no answer
			}
			ping, err := wire.ParsePing(buf[:n])
			if err != nil {
				h.Log.Debug("refused a ping", "outpost", name, "error", err)
				continue
			}
			l.Heard(name)
			l.SetVersion(name, ping.Version)
			if pong, err := wire.AppendPong(nil, wire.Pong{Version: h.Version}); err == nil {
				conn.WriteTo(pong, from)
			}
		}
	}()

	poll := h.Poll
	if poll == 0 {
		poll = defaultLivenessPoll
	}
	go func() {
		rx := map[string]uint64{}
		t := time.NewTicker(poll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			pollWireGuard(l, tun, store.Snapshot(), rx)
			l.Check()
		}
	}()
	return nil
}

// pollWireGuard counts bytes arriving from a peer as hearing from its Outpost,
// and takes the peer's endpoint as the Outpost's. rx remembers the last count.
func pollWireGuard(l *liveness.Liveness, tun *tunnel.Tunnel, st *state.State, rx map[string]uint64) {
	out, err := tun.Dev.IpcGet()
	if err != nil {
		return
	}
	names := map[string]string{} // public key hex -> Outpost name
	for _, o := range st.Outposts {
		names[o.PublicKey.Hex()] = o.Name
	}
	for _, block := range strings.Split(out, "public_key=")[1:] {
		lines := strings.Split(block, "\n")
		name, ok := names[lines[0]]
		if !ok {
			continue
		}
		var bytes uint64
		var endpoint string
		for _, ln := range lines[1:] {
			if v, ok := strings.CutPrefix(ln, "rx_bytes="); ok {
				fmt.Sscan(v, &bytes)
			} else if v, ok := strings.CutPrefix(ln, "endpoint="); ok {
				endpoint = v
			}
		}
		if bytes > rx[name] {
			l.Heard(name)
		}
		rx[name] = bytes
		if ep, err := netip.ParseAddrPort(endpoint); err == nil {
			l.SetEndpoint(name, ep)
		}
	}
}
