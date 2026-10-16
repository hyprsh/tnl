// Package conntrack knows every Tunnel connection the Edge's transport has
// open, by Route and by Outpost, idle pooled ones included. Its state lives
// outside Caddy's modules so it survives reloads, and it imports no Caddy.
//
// Closing is the caller's to do after the state store's lock is released,
// never under it: a close can block on the network.
package conntrack

import (
	"net"
	"sync"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

// Tracker is the set of open connections. A nil *Tracker tracks nothing and
// closes nothing.
type Tracker struct {
	mu    sync.Mutex
	conns map[*conn]struct{}
}

// New returns an empty Tracker.
func New() *Tracker { return &Tracker{conns: map[*conn]struct{}{}} }

type conn struct {
	net.Conn
	t        *Tracker
	hostname string // normalised
	outpost  string
	once     sync.Once
}

func (c *conn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		c.t.mu.Lock()
		delete(c.t.conns, c)
		c.t.mu.Unlock()
	})
	return err
}

// Track registers c as a connection of the Route holding hostname, through the
// named Outpost, and returns the connection to use in its place. The Tracker
// forgets it when it is closed.
func (t *Tracker) Track(hostname, outpost string, c net.Conn) net.Conn {
	if t == nil {
		return c
	}
	tc := &conn{Conn: c, t: t, hostname: routes.Normalize(hostname), outpost: outpost}
	t.mu.Lock()
	t.conns[tc] = struct{}{}
	t.mu.Unlock()
	return tc
}

// CloseRoute closes every connection of the Route holding hostname.
func (t *Tracker) CloseRoute(hostname string) {
	h := routes.Normalize(hostname)
	t.closeWhere(func(c *conn) bool { return c.hostname == h })
}

// CloseOutpost closes every connection through the named Outpost.
func (t *Tracker) CloseOutpost(name string) {
	t.closeWhere(func(c *conn) bool { return c.outpost == name })
}

// CloseAll closes every connection: the drain on SIGTERM.
func (t *Tracker) CloseAll() { t.closeWhere(func(*conn) bool { return true }) }

// Count is the number of open connections.
func (t *Tracker) Count() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.conns)
}

func (t *Tracker) closeWhere(match func(*conn) bool) {
	if t == nil {
		return
	}
	var doomed []*conn
	t.mu.Lock()
	for c := range t.conns {
		if match(c) {
			doomed = append(doomed, c)
		}
	}
	t.mu.Unlock()
	for _, c := range doomed {
		c.Close()
	}
}
