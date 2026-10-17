// Package tunnel runs userspace WireGuard on tnl's own netstack device. Both the
// Edge and an Outpost use it. It needs no root, TUN device or kernel module.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"

	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/tunnel/netstack"
)

// MTU used on both ends.
const MTU = 1420

// OutpostKeepalive is the Outpost's WireGuard persistent keepalive, in seconds.
const OutpostKeepalive = 25

// Config describes one end of the Tunnel.
type Config struct {
	Address    netip.Addr // this end's Tunnel address
	PrivateKey keys.Key
	ListenPort int // 0 picks a free port
	// Logger receives wireguard-go's messages; see logx.WireGuardLogger. Nil
	// discards them.
	Logger *device.Logger
}

// Peer is the other end.
type Peer struct {
	PublicKey keys.Key
	AllowedIP netip.Addr     // the peer's Tunnel address, as a /32; unset adds none
	Endpoint  netip.AddrPort // must be an IP, not a name; unset on the Edge
	// KeepaliveSeconds is persistent_keepalive_interval; 0 leaves it off.
	KeepaliveSeconds int
}

// Tunnel is a running WireGuard device and its netstack.
type Tunnel struct {
	Dev *device.Device
	Net *netstack.Net
}

// New starts a Tunnel with no peers.
func New(cfg Config) (*Tunnel, error) {
	tdev, tnet, err := netstack.CreateNetTUN([]netip.Addr{cfg.Address}, MTU)
	if err != nil {
		return nil, err
	}
	// wireguard-go panics on a nil log function.
	logger := cfg.Logger
	if logger == nil {
		logger = &device.Logger{Verbosef: device.DiscardLogf, Errorf: device.DiscardLogf}
	}
	dev := device.NewDevice(tdev, conn.NewDefaultBind(), logger)
	conf := fmt.Sprintf("private_key=%s\nlisten_port=%d\n", cfg.PrivateKey.Hex(), cfg.ListenPort)
	if err := dev.IpcSet(conf); err != nil {
		dev.Close()
		return nil, fmt.Errorf("configure wireguard: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("bring wireguard up: %w", err)
	}
	return &Tunnel{Dev: dev, Net: tnet}, nil
}

// AddPeer adds or updates a peer.
func (t *Tunnel) AddPeer(p Peer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "public_key=%s\n", p.PublicKey.Hex())
	if p.AllowedIP.IsValid() {
		fmt.Fprintf(&b, "allowed_ip=%s/32\n", p.AllowedIP)
	}
	if p.Endpoint.IsValid() {
		fmt.Fprintf(&b, "endpoint=%s\n", p.Endpoint)
	}
	if p.KeepaliveSeconds > 0 {
		fmt.Fprintf(&b, "persistent_keepalive_interval=%d\n", p.KeepaliveSeconds)
	}
	return t.Dev.IpcSet(b.String())
}

// SetEndpoint points an existing peer at a new endpoint and touches nothing else.
func (t *Tunnel) SetEndpoint(key keys.Key, ep netip.AddrPort) error {
	return t.Dev.IpcSet(fmt.Sprintf("public_key=%s\nupdate_only=true\nendpoint=%s\n", key.Hex(), ep))
}

// RemovePeer removes a peer; one that isn't there is not an error.
func (t *Tunnel) RemovePeer(key keys.Key) error {
	return t.Dev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", key.Hex()))
}

// SetPrivateKey switches this end to a new key. Peers keep their settings; the
// next packet handshakes afresh.
func (t *Tunnel) SetPrivateKey(key keys.Key) error {
	return t.Dev.IpcSet(fmt.Sprintf("private_key=%s\n", key.Hex()))
}

// ListenPort reports the UDP port WireGuard bound.
func (t *Tunnel) ListenPort() (int, error) {
	if v, ok := t.get("listen_port"); ok {
		return strconv.Atoi(v)
	}
	return 0, errors.New("no listen_port in wireguard state")
}

// Each calls fn with every key=value line of WireGuard's state, in order. A
// peer's lines follow its public_key line.
func (t *Tunnel) Each(fn func(key, value string)) error {
	out, err := t.Dev.IpcGet()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			fn(k, v)
		}
	}
	return nil
}

func (t *Tunnel) get(key string) (value string, found bool) {
	t.Each(func(k, v string) {
		if k == key && !found {
			value, found = v, true
		}
	})
	return value, found
}

// WaitHandshake blocks until some peer has completed a handshake.
func (t *Tunnel) WaitHandshake(ctx context.Context) error {
	for {
		handshook := false
		err := t.Each(func(k, v string) {
			if k == "last_handshake_time_sec" && v != "0" {
				handshook = true
			}
		})
		if err != nil {
			return err
		}
		if handshook {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for a WireGuard handshake: %w", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// txBytes is the bytes WireGuard has sent to all peers, which it counts after
// the UDP send.
func (t *Tunnel) txBytes() (total int64) {
	t.Each(func(k, v string) {
		if k == "tx_bytes" {
			n, _ := strconv.ParseInt(v, 10, 64)
			total += n
		}
	})
	return total
}

// quietPeriod is how long the Tunnel must send nothing, with nothing queued, for
// Drain to call it drained.
const quietPeriod = 30 * time.Millisecond

// Drain waits until what the netstack has sent has left through WireGuard: no
// packet queued in the stack and no byte sent for a quiet period. It gives up
// after max, so an unreachable peer cannot hold it. Call it before Close, which
// drops whatever is still queued.
func (t *Tunnel) Drain(max time.Duration) {
	deadline := time.Now().Add(max)
	last, quietSince := t.txBytes(), time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		if tx := t.txBytes(); tx != last || t.Net.Queued() > 0 {
			last, quietSince = tx, time.Now()
		} else if time.Since(quietSince) >= quietPeriod {
			return
		}
	}
}

// Close stops WireGuard and the netstack.
func (t *Tunnel) Close() { t.Dev.Close() }
