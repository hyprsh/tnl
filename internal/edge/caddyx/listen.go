package caddyx

import (
	"context"
	"net"
	"strconv"
	"sync"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// listenNetwork is the Caddy network the Edge listens on, instead of "tcp".
//
// On Unix, Caddy binds a new SO_REUSEPORT socket for each config and closes the
// old config's socket after the new one is up, and the kernel resets every
// connection still waiting in the old socket's accept queue. That is a Visitor
// reset by every Zone edit. This network keeps one socket per address for as
// long as any config uses it, and hands each config a view of it: a reload
// moves the accepting to the new config's view, and no connection is dropped.
const listenNetwork = "tnltcp"

func init() {
	caddy.RegisterNetwork(listenNetwork, func(ctx context.Context, _, host, port string, offset uint, cfg net.ListenConfig) (any, error) {
		p, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return nil, err
		}
		return sharedListen(ctx, net.JoinHostPort(host, strconv.FormatUint(p+uint64(offset), 10)), cfg)
	})
	// HTTP/3 is UDP, which has no accept queue to reset.
	caddyhttp.RegisterNetworkHTTP3(listenNetwork, "udp")
}

// listenAddr turns a listen address such as ":443" into one on listenNetwork.
func listenAddr(a string) string { return listenNetwork + "/" + a }

var (
	sharedMu sync.Mutex
	shared   = map[string]*sharedListener{}
)

func sharedListen(ctx context.Context, addr string, cfg net.ListenConfig) (net.Listener, error) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	s := shared[addr]
	if s == nil {
		ln, err := cfg.Listen(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		s = &sharedListener{addr: addr, ln: ln, wake: make(chan struct{})}
		shared[addr] = s
		v := s.view()
		go s.pump()
		return v, nil
	}
	return s.view(), nil
}

// sharedListener is one socket, accepted by one goroutine, and handed to the
// newest open view.
type sharedListener struct {
	addr string
	ln   net.Listener

	mu    sync.Mutex // guards views and wake
	views []*view    // open views, oldest first
	wake  chan struct{}
}

func (s *sharedListener) view() *view {
	v := &view{s: s, conns: make(chan net.Conn), closed: make(chan struct{})}
	s.mu.Lock()
	s.views = append(s.views, v)
	s.signal()
	s.mu.Unlock()
	return v
}

// signal wakes the pump. The caller holds s.mu.
func (s *sharedListener) signal() {
	close(s.wake)
	s.wake = make(chan struct{})
}

// newest returns the view to hand a connection to, and a channel that closes
// when that changes.
func (s *sharedListener) newest() (*view, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.views) == 0 {
		return nil, s.wake
	}
	return s.views[len(s.views)-1], s.wake
}

func (s *sharedListener) pump() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			// A listener that fails for good (it was closed) ends every view.
			s.mu.Lock()
			for _, v := range s.views {
				v.fail(err)
			}
			s.mu.Unlock()
			return
		}
		for delivered := false; !delivered; {
			v, changed := s.newest()
			if v == nil {
				// The last view closed, and the socket with it.
				c.Close()
				break
			}
			select {
			case v.conns <- c:
				delivered = true
			case <-v.closed:
			case <-changed:
			}
		}
	}
}

// remove drops a closed view, and closes the socket with the last one.
func (s *sharedListener) remove(v *view) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	s.mu.Lock()
	for i, w := range s.views {
		if w == v {
			s.views = append(s.views[:i:i], s.views[i+1:]...)
			break
		}
	}
	last := len(s.views) == 0
	s.signal()
	s.mu.Unlock()
	if last && shared[s.addr] == s {
		delete(shared, s.addr)
		s.ln.Close()
	}
}

// view is what one config holds. Closing it stops its own Accept and leaves the
// socket open for the next config.
type view struct {
	s      *sharedListener
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
	err    error
}

func (v *view) fail(err error) {
	v.once.Do(func() { v.err = err; close(v.closed) })
}

func (v *view) Accept() (net.Conn, error) {
	select {
	case c := <-v.conns:
		return c, nil
	case <-v.closed:
		if v.err != nil {
			return nil, v.err
		}
		return nil, net.ErrClosed
	}
}

func (v *view) Close() error {
	first := false
	v.once.Do(func() { first = true; close(v.closed) })
	if first {
		v.s.remove(v)
	}
	return nil
}

func (v *view) Addr() net.Addr { return v.s.ln.Addr() }
