package edge

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/declare"
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
// from every Outpost the state holds that has connected before. The saved
// endpoint is the sign of that: it is written when the Edge first hears from
// the Outpost, before it counts it online. One without it has never connected, and is not counted online.
func newLiveness(h livenessHooks, st *state.State) *liveness.Liveness {
	l := liveness.New(liveness.Config{EdgeVersion: h.Version, OfflineAfter: h.OfflineAfter, Log: h.Log, OnOffline: h.CloseOutpost})
	for _, o := range st.Outposts {
		switch {
		case o.OneTimeKey != nil:
		case o.Endpoint.IsValid():
			l.Seed(o.Name)
		default:
			l.SeedNever(o.Name)
		}
	}
	return l
}

// serveLiveness listens for pings on 100.64.0.1:wire.PingPort inside the Tunnel,
// and watches WireGuard for any other traffic from an Outpost and for its
// endpoint. Both stop when ctx ends; bg tracks their goroutines, so the caller
// can wait until none is left that could still write the state.
func serveLiveness(ctx context.Context, bg *sync.WaitGroup, l *liveness.Liveness, h livenessHooks, tun *tunnel.Tunnel, store *state.Store) error {
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
	bg.Go(func() {
		<-ctx.Done()
		conn.Close()
	})
	pingRemembered(conn, h.Version, store.Snapshot(), h.Log)
	keep := &endpointKeeper{store: store, tried: map[string]netip.AddrPort{}}
	bg.Go(func() {
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
			// The first time an Outpost is heard from, save its endpoint before
			// counting it online, so a crash after that still shows it connected.
			if !hasEndpoint(store.Snapshot(), name) {
				keep.remember(scanPeers(tun, store.Snapshot()).endpoints())
			}
			l.Heard(name)
			l.SetVersion(name, ping.Version)
			inside := l.State(name) != liveness.Incompatible
			if pong, err := wire.AppendPong(nil, answerPing(h.Version, inside, store.Snapshot(), name, ping)); err == nil {
				conn.WriteTo(pong, from)
			}
		}
	})

	poll := h.Poll
	if poll == 0 {
		poll = defaultLivenessPoll
	}
	bg.Go(func() {
		rx := map[string]uint64{}
		t := time.NewTicker(poll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				// One last look, so an Outpost that handshook within the last
				// poll is remembered as having connected. The device is still
				// open: it closes after bg is done.
				pollWireGuard(l, tun, store.Snapshot(), rx, keep)
				return
			case <-t.C:
			}
			pollWireGuard(l, tun, store.Snapshot(), rx, keep)
			l.Check()
		}
	})
	return nil
}

// answerPing is the Edge's answer to an Outpost's ping. A ping with a
// Declaration digest is told whether it matches the stored copy, and the digest
// of the Outpost's Routes and waiting lines as the Edge has them.
func answerPing(version string, inside bool, st *state.State, outpost string, ping wire.Ping) wire.Pong {
	pong := wire.Pong{Version: version, Inside: inside}
	if ping.Declaration != nil {
		stored := st.DeclarationDigest(outpost)
		pong.Declaration = &wire.DeclarationAnswer{
			Matches: stored == [declare.DigestSize]byte(*ping.Declaration),
			Routes:  declare.RoutesDigest(st.Entries(outpost)),
		}
	}
	return pong
}

// peer is what WireGuard reports about one Outpost's peer.
type peer struct {
	name     string
	rxBytes  uint64
	endpoint netip.AddrPort // invalid until the peer has been heard
}

type peers []peer

// endpoints maps each Outpost name to its peer's endpoint, where it has one.
func (ps peers) endpoints() map[string]netip.AddrPort {
	m := map[string]netip.AddrPort{}
	for _, p := range ps {
		if p.endpoint.IsValid() {
			m[p.name] = p.endpoint
		}
	}
	return m
}

// scanPeers reads the device's peers that belong to an Outpost in st.
func scanPeers(tun *tunnel.Tunnel, st *state.State) peers {
	names := map[string]string{} // public key hex -> Outpost name
	for _, o := range st.Outposts {
		names[o.PublicKey.Hex()] = o.Name
	}
	var out peers
	cur := -1
	err := tun.Each(func(key, value string) {
		switch key {
		case "public_key":
			cur = -1
			if name, ok := names[value]; ok {
				out = append(out, peer{name: name})
				cur = len(out) - 1
			}
		case "rx_bytes":
			if cur >= 0 {
				fmt.Sscan(value, &out[cur].rxBytes)
			}
		case "endpoint":
			if cur >= 0 {
				out[cur].endpoint, _ = netip.ParseAddrPort(value)
			}
		}
	})
	if err != nil {
		return nil
	}
	return out
}

func hasEndpoint(st *state.State, name string) bool {
	for _, o := range st.Outposts {
		if o.Name == name {
			return o.Endpoint.IsValid()
		}
	}
	return true // not an Outpost: nothing to save
}

// pollWireGuard saves each Outpost's new endpoint, then counts bytes arriving
// from a peer as hearing from its Outpost and takes the peer's endpoint as the
// Outpost's. The save comes first, so an Outpost is never counted online before
// its endpoint is kept. rx remembers the last byte count.
func pollWireGuard(l *liveness.Liveness, tun *tunnel.Tunnel, st *state.State, rx map[string]uint64, keep *endpointKeeper) {
	ps := scanPeers(tun, st)
	keep.remember(ps.endpoints())
	for _, p := range ps {
		if p.rxBytes > rx[p.name] {
			l.Heard(p.name)
		}
		rx[p.name] = p.rxBytes
		if p.endpoint.IsValid() {
			l.SetEndpoint(p.name, p.endpoint)
		}
	}
}

// endpointKeeper saves an Outpost's endpoint in the state when it differs from
// the saved one, so a restarted Edge can ping it at once. A save is tried once
// per change: tried holds the last endpoint attempted for each Outpost. The
// ping handler and the poll both use it.
type endpointKeeper struct {
	store *state.Store
	mu    sync.Mutex
	tried map[string]netip.AddrPort
}

func (k *endpointKeeper) remember(seen map[string]netip.AddrPort) {
	k.mu.Lock()
	defer k.mu.Unlock()
	var changed []string
	for _, o := range k.store.Snapshot().Outposts {
		if ep, ok := seen[o.Name]; ok && ep != o.Endpoint && k.tried[o.Name] != ep {
			changed = append(changed, o.Name)
		}
	}
	if len(changed) == 0 {
		return
	}
	for _, name := range changed {
		k.tried[name] = seen[name]
	}
	// A refused save is warned about by the store.
	k.store.Update(api.ActorLocal, func(st *state.State) error {
		for i := range st.Outposts {
			if ep, ok := seen[st.Outposts[i].Name]; ok && slices.Contains(changed, st.Outposts[i].Name) {
				st.Outposts[i].Endpoint = ep
			}
		}
		return nil
	})
}

// pingRemembered pings every Outpost with a remembered endpoint from the ping
// socket. The ping reaches the Outpost's ping port through WireGuard, which
// handshakes with the saved endpoint to send it, so it is also the Edge's first
// handshake. The Outpost answers with a ping of its own, which tells the Edge
// its version.
func pingRemembered(conn *gonet.UDPConn, version string, st *state.State, log *slog.Logger) {
	ping, err := wire.AppendPing(nil, wire.Ping{Version: version})
	if err != nil {
		return
	}
	for _, o := range st.Outposts {
		if o.OneTimeKey != nil || !o.Endpoint.IsValid() {
			continue
		}
		if _, err := conn.WriteTo(ping, net.UDPAddrFromAddrPort(netip.AddrPortFrom(o.Address, wire.PingPort))); err != nil {
			log.Debug("could not ping a remembered endpoint", "outpost", o.Name, "error", err)
		}
	}
}
