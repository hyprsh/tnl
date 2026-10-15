// Package forwarder is the Outpost's data-port listener. For each connection it
// reads the tnl header, dials the Service on the host's network, replies, and
// copies bytes both ways.
package forwarder

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/hyprsh/tnl/internal/wire"
)

// Defaults from the spec's constants table.
const (
	DefaultDialTimeout   = 5 * time.Second
	DefaultHeaderTimeout = 10 * time.Second
)

// Dialer opens a connection to the Service. The default uses the host's
// resolver and network.
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// Forwarder serves tnl connections. The zero value uses the defaults.
type Forwarder struct {
	Version       string        // this Outpost's version, sent in an unsupported reply
	Dialer        Dialer        // nil dials with the host's resolver and network
	DialTimeout   time.Duration // 0 means DefaultDialTimeout
	HeaderTimeout time.Duration // 0 means DefaultHeaderTimeout

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// track remembers c until the returned func is called, so Serve can close
// connections still open when it stops.
func (f *Forwarder) track(c net.Conn) func() {
	f.mu.Lock()
	if f.conns == nil {
		f.conns = map[net.Conn]struct{}{}
	}
	f.conns[c] = struct{}{}
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		delete(f.conns, c)
		f.mu.Unlock()
	}
}

func (f *Forwarder) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for c := range f.conns {
		c.Close()
	}
}

// Serve accepts connections on ln until it is closed.
func (f *Forwarder) Serve(ln net.Listener) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	defer f.closeAll()
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.Handle(c)
		}()
	}
}

// Handle serves one connection and closes it.
func (f *Forwarder) Handle(c net.Conn) {
	defer c.Close()
	defer f.track(c)()
	hdrTimeout := orDefault(f.HeaderTimeout, DefaultHeaderTimeout)
	c.SetReadDeadline(time.Now().Add(hdrTimeout))
	h, err := wire.ReadHeader(c)
	if err != nil {
		if isRefusal(err) {
			wire.WriteReply(c, wire.Reply{Status: wire.StatusUnsupported, Version: f.Version})
		}
		return
	}
	c.SetReadDeadline(time.Time{})

	ctx, cancel := context.WithTimeout(context.Background(), orDefault(f.DialTimeout, DefaultDialTimeout))
	dial := f.Dialer
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	svc, err := dial(ctx, "tcp", h.Service)
	cancel()
	if err != nil {
		wire.WriteReply(c, wire.Reply{Status: wire.StatusDialError, Reason: Classify(err)})
		return
	}
	defer svc.Close()
	defer f.track(svc)()
	if err := wire.WriteReply(c, wire.Reply{Status: wire.StatusOK}); err != nil {
		return
	}
	pipe(c, svc)
}

func isRefusal(err error) bool {
	for _, e := range []error{wire.ErrBadMarker, wire.ErrUnknownFormat, wire.ErrUnknownField, wire.ErrFieldTooLong, wire.ErrMissingField, wire.ErrDuplicate} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// Classify maps a dial error to one of the four reasons. An unreachable network
// counts as refused, because the spec has no other reason for it.
func Classify(err error) wire.Reason {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return wire.ReasonDNS
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return wire.ReasonRefused
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return wire.ReasonTimeout
	}
	return wire.ReasonRefused
}

func orDefault(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

type closeWriter interface{ CloseWrite() error }

// pipe copies both ways until both directions finish, passing half-closes on.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
		if cw, ok := dst.(closeWriter); ok {
			cw.CloseWrite()
		} else {
			dst.Close()
		}
	}
	wg.Add(2)
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
}
