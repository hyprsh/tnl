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
// Outpost's Tunnel address, and reads the Edge's answers. It returns when ctx
// ends. A ping that fails to send or an answer that fails to parse is only
// logged at debug: the Edge decides what silence means.
func runPinger(ctx context.Context, tun *tunnel.Tunnel, self netip.Addr, version string, interval time.Duration, log *slog.Logger) error {
	conn, err := tun.Net.DialUDPAddrPort(netip.AddrPortFrom(self, 0), netip.AddrPortFrom(addr.Edge, wire.PingPort))
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
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
			if pong, err := wire.ParsePong(buf[:n]); err != nil {
				log.Debug("refused the Edge's answer to a ping", "error", err)
			} else {
				log.Debug("the Edge answered a ping", "edge_version", pong.Version)
			}
		}
	}()
	ping, err := wire.AppendPing(nil, wire.Ping{Version: version})
	if err != nil {
		return err
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			if _, err := conn.Write(ping); err != nil {
				log.Debug("could not send a ping", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return nil
}
