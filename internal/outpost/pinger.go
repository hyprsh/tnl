package outpost

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/tunnel"
	"github.com/hyprsh/tnl/internal/wire"
)

// DefaultPingInterval is how often the Outpost pings the Edge.
const DefaultPingInterval = 10 * time.Second

// runPinger pings 100.64.0.1 on the ping port every interval, from the
// Outpost's Tunnel address and the ping port, and reads the Edge's answers,
// calling answered with each. The ping carries the digest of the Outpost's
// Declaration. A ping from the Edge, which
// it sends at its start to every remembered endpoint, is answered with a ping at
// once, so the Edge learns this Outpost's version within a round trip. It
// returns when ctx ends. A ping that fails to send or an answer that fails to
// parse is only logged at debug: the Edge decides what silence means.
func runPinger(ctx context.Context, tun *tunnel.Tunnel, self netip.Addr, version string, declaration *wire.Digest, interval time.Duration, answered func(wire.Pong), log *slog.Logger) error {
	conn, err := tun.Net.DialUDPAddrPort(netip.AddrPortFrom(self, wire.PingPort), netip.AddrPortFrom(addr.Edge, wire.PingPort))
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	ping, err := wire.AppendPing(nil, wire.Ping{Version: version, Declaration: declaration})
	if err != nil {
		return err
	}
	send := func() {
		if _, err := conn.Write(ping); err != nil {
			log.Debug("could not send a ping", "error", err)
		}
	}
	go func() {
		buf := make([]byte, 512)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				if ctx.Err() != nil || err == net.ErrClosed {
					return
				}
				continue
			}
			if _, err := wire.ParsePing(buf[:n]); err == nil {
				send() // the Edge's own ping: answer with ours
				continue
			}
			if pong, err := wire.ParsePong(buf[:n]); err != nil {
				log.Debug("refused a datagram from the Edge", "error", err)
			} else {
				log.Debug("the Edge answered a ping", "edge_version", pong.Version)
				answered(pong)
			}
		}
	}()
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			send()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return nil
}
