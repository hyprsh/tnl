// Derived from gVisor's tcpip/adapters/gonet (Apache-2.0): the dial and listen
// code, with TCP keepalive set on every connection.

package netstack

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// TCP keepalive on every Tunnel connection, dialled or accepted, on both sides:
// a connection idle for KeepaliveIdle is probed every KeepaliveInterval and
// dropped after KeepaliveProbes unanswered probes. There is no TCP user timeout.
// They are variables so that a test can shrink them.
var (
	KeepaliveIdle     = 30 * time.Second
	KeepaliveInterval = 10 * time.Second
	KeepaliveProbes   = 3
)

// Conn is a Tunnel TCP connection.
type Conn struct {
	*gonet.TCPConn
	ep tcpip.Endpoint
}

// Keepalive reports the connection's keepalive settings.
func (c *Conn) Keepalive() (on bool, idle, interval time.Duration, probes int, err error) {
	on = c.ep.SocketOptions().GetKeepAlive()
	var i tcpip.KeepaliveIdleOption
	var n tcpip.KeepaliveIntervalOption
	if e := c.ep.GetSockOpt(&i); e != nil {
		return false, 0, 0, 0, fmt.Errorf("read keepalive idle: %v", e)
	}
	if e := c.ep.GetSockOpt(&n); e != nil {
		return false, 0, 0, 0, fmt.Errorf("read keepalive interval: %v", e)
	}
	count, e := c.ep.GetSockOptInt(tcpip.KeepaliveCountOption)
	if e != nil {
		return false, 0, 0, 0, fmt.Errorf("read keepalive count: %v", e)
	}
	return on, time.Duration(i), time.Duration(n), count, nil
}

func setKeepalive(ep tcpip.Endpoint) error {
	ep.SocketOptions().SetKeepAlive(true)
	idle := tcpip.KeepaliveIdleOption(KeepaliveIdle)
	interval := tcpip.KeepaliveIntervalOption(KeepaliveInterval)
	if err := ep.SetSockOpt(&idle); err != nil {
		return fmt.Errorf("keepalive idle: %v", err)
	}
	if err := ep.SetSockOpt(&interval); err != nil {
		return fmt.Errorf("keepalive interval: %v", err)
	}
	if err := ep.SetSockOptInt(tcpip.KeepaliveCountOption, KeepaliveProbes); err != nil {
		return fmt.Errorf("keepalive probes: %v", err)
	}
	return nil
}

// dialTCP is gonet.DialContextTCP with keepalive set before the connect.
func dialTCP(ctx context.Context, s *stack.Stack, remote tcpip.FullAddress, network tcpip.NetworkProtocolNumber) (*Conn, error) {
	var wq waiter.Queue
	ep, terr := s.NewEndpoint(tcp.ProtocolNumber, network, &wq)
	if terr != nil {
		return nil, errors.New(terr.String())
	}
	if err := setKeepalive(ep); err != nil {
		ep.Close()
		return nil, err
	}
	waitEntry, notifyCh := waiter.NewChannelEntry(waiter.WritableEvents)
	wq.EventRegister(&waitEntry)
	defer wq.EventUnregister(&waitEntry)

	select {
	case <-ctx.Done():
		ep.Close()
		return nil, ctx.Err()
	default:
	}
	terr = ep.Connect(remote)
	if _, ok := terr.(*tcpip.ErrConnectStarted); ok {
		select {
		case <-ctx.Done():
			ep.Close()
			return nil, ctx.Err()
		case <-notifyCh:
		}
		terr = ep.LastError()
	}
	if terr != nil {
		ep.Close()
		return nil, &net.OpError{Op: "connect", Net: "tcp", Addr: fullToTCPAddr(remote), Err: errors.New(terr.String())}
	}
	return &Conn{TCPConn: gonet.NewTCPConn(&wq, ep), ep: ep}, nil
}

// Listener accepts Tunnel connections with keepalive set.
type Listener struct {
	ep         tcpip.Endpoint
	wq         *waiter.Queue
	cancelOnce sync.Once
	cancel     chan struct{}
}

const maxListenBacklog = 4096

func listenTCP(s *stack.Stack, addr tcpip.FullAddress, network tcpip.NetworkProtocolNumber) (*Listener, error) {
	var wq waiter.Queue
	ep, terr := s.NewEndpoint(tcp.ProtocolNumber, network, &wq)
	if terr != nil {
		return nil, errors.New(terr.String())
	}
	if terr := ep.Bind(addr); terr != nil {
		ep.Close()
		return nil, &net.OpError{Op: "bind", Net: "tcp", Addr: fullToTCPAddr(addr), Err: errors.New(terr.String())}
	}
	if terr := ep.Listen(maxListenBacklog); terr != nil {
		ep.Close()
		return nil, &net.OpError{Op: "listen", Net: "tcp", Addr: fullToTCPAddr(addr), Err: errors.New(terr.String())}
	}
	return &Listener{ep: ep, wq: &wq, cancel: make(chan struct{})}, nil
}

// Accept implements net.Listener.
func (l *Listener) Accept() (net.Conn, error) {
	c, err := l.AcceptConn()
	if err != nil {
		return nil, err
	}
	return c, nil
}

// AcceptConn is Accept returning the *Conn.
func (l *Listener) AcceptConn() (*Conn, error) {
	n, wq, terr := l.ep.Accept(nil)
	if _, ok := terr.(*tcpip.ErrWouldBlock); ok {
		waitEntry, notifyCh := waiter.NewChannelEntry(waiter.ReadableEvents)
		l.wq.EventRegister(&waitEntry)
		defer l.wq.EventUnregister(&waitEntry)
		for {
			n, wq, terr = l.ep.Accept(nil)
			if _, ok := terr.(*tcpip.ErrWouldBlock); !ok {
				break
			}
			select {
			case <-l.cancel:
				return nil, net.ErrClosed
			case <-notifyCh:
			}
		}
	}
	if terr != nil {
		return nil, &net.OpError{Op: "accept", Net: "tcp", Addr: l.Addr(), Err: errors.New(terr.String())}
	}
	if err := setKeepalive(n); err != nil {
		n.Close()
		return nil, err
	}
	return &Conn{TCPConn: gonet.NewTCPConn(wq, n), ep: n}, nil
}

// Close implements net.Listener.
func (l *Listener) Close() error {
	l.cancelOnce.Do(func() { close(l.cancel) })
	l.ep.Close()
	return nil
}

// Addr implements net.Listener.
func (l *Listener) Addr() net.Addr {
	a, err := l.ep.GetLocalAddress()
	if err != nil {
		return nil
	}
	return fullToTCPAddr(a)
}

func fullToTCPAddr(a tcpip.FullAddress) *net.TCPAddr {
	ip, _ := netip.AddrFromSlice(a.Addr.AsSlice())
	return &net.TCPAddr{IP: ip.AsSlice(), Port: int(a.Port)}
}
