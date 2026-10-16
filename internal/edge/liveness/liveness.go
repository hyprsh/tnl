// Package liveness is the one place the Edge keeps what it knows about each
// Outpost's reachability: when it was last heard from, which version it runs,
// and its public endpoint. All of it is runtime fact, never saved. Slice 7 adds
// Incompatible here, and the status commands read it through Info and State.
//
// It imports no Caddy, so the API server and the transport both take it.
package liveness

import (
	"log/slog"
	"net/netip"
	"sync"
	"time"
)

// DefaultOfflineAfter is the silence after which an Outpost is Offline.
const DefaultOfflineAfter = 60 * time.Second

// State is what the Edge thinks of an Outpost.
type State int

const (
	// Unknown is an Outpost never heard from and not seeded: not joined yet.
	Unknown State = iota
	Online
	Offline
)

func (s State) String() string {
	switch s {
	case Online:
		return "online"
	case Offline:
		return "offline"
	}
	return "unknown"
}

// Config tunes a Liveness. The zero value of each field is its default.
type Config struct {
	OfflineAfter time.Duration    // 60 s
	Now          func() time.Time // the clock; time.Now
	Log          *slog.Logger     // transitions; nil logs nothing
	// OnOffline runs, with no lock held, once each time an Outpost goes Offline.
	OnOffline func(name string)
}

// Info is a read of one Outpost.
type Info struct {
	State    State
	LastSeen time.Time      // zero when never heard
	Endpoint netip.AddrPort // the public endpoint last seen; zero when unknown
	Version  string         // from the last ping; empty before the first
}

// Liveness tracks the Outposts. It is safe for concurrent use.
type Liveness struct {
	cfg Config
	mu  sync.Mutex
	m   map[string]*entry
}

type entry struct {
	lastSeen time.Time
	endpoint netip.AddrPort
	version  string
	offline  bool // the Offline transition has been taken
}

// New returns an empty Liveness.
func New(cfg Config) *Liveness {
	if cfg.OfflineAfter == 0 {
		cfg.OfflineAfter = DefaultOfflineAfter
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Liveness{cfg: cfg, m: map[string]*entry{}}
}

// Seed counts as hearing from the Outpost now, unless it is tracked already:
// the Edge's own start counts as hearing from every Outpost.
func (l *Liveness) Seed(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.m[name]; !ok {
		l.m[name] = &entry{lastSeen: l.cfg.Now()}
	}
}

// Heard records that something arrived from the Outpost: a ping or any data
// packet. If it was Offline, it is back online, and the outage is logged.
func (l *Liveness) Heard(name string) {
	now := l.cfg.Now()
	l.mu.Lock()
	e := l.m[name]
	if e == nil {
		e = new(entry)
		l.m[name] = e
		e.lastSeen = now
		l.mu.Unlock()
		return
	}
	wentOffline := l.expire(e, now)
	was := e.lastSeen
	back := e.offline
	e.offline = false
	e.lastSeen = now
	l.mu.Unlock()
	if wentOffline {
		l.fireOffline(name, was)
	}
	if back {
		l.cfg.Log.Info("outpost back online", "outpost", name, "gone", now.Sub(was))
	}
}

// SetVersion records the version the Outpost's ping reported.
func (l *Liveness) SetVersion(name, version string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.m[name]; e != nil {
		e.version = version
	}
}

// SetEndpoint records the Outpost's public endpoint, logging it at info when
// it is new.
func (l *Liveness) SetEndpoint(name string, ep netip.AddrPort) {
	if !ep.IsValid() {
		return
	}
	l.mu.Lock()
	e := l.m[name]
	changed := e != nil && e.endpoint != ep
	if changed {
		e.endpoint = ep
	}
	l.mu.Unlock()
	if changed {
		l.cfg.Log.Info("outpost has a new public endpoint", "outpost", name, "endpoint", ep)
	}
}

// Check takes the Offline transition for every Outpost silent for too long.
func (l *Liveness) Check() {
	now := l.cfg.Now()
	type gone struct {
		name string
		last time.Time
	}
	var out []gone
	l.mu.Lock()
	for name, e := range l.m {
		if l.expire(e, now) {
			out = append(out, gone{name, e.lastSeen})
		}
	}
	l.mu.Unlock()
	for _, g := range out {
		l.fireOffline(g.name, g.last)
	}
}

// expire marks e Offline if it is due and reports whether it did. The caller
// holds the lock and calls fireOffline after releasing it.
func (l *Liveness) expire(e *entry, now time.Time) bool {
	if e.offline || now.Sub(e.lastSeen) < l.cfg.OfflineAfter {
		return false
	}
	e.offline = true
	return true
}

func (l *Liveness) fireOffline(name string, lastSeen time.Time) {
	l.cfg.Log.Warn("outpost offline", "outpost", name, "last_seen", lastSeen.UTC().Format(time.RFC3339))
	if l.cfg.OnOffline != nil {
		l.cfg.OnOffline(name)
	}
}

// State is the Outpost's state, Offline from the moment its window is up.
func (l *Liveness) State(name string) State { return l.Info(name).State }

// Info reads everything known about the Outpost.
func (l *Liveness) Info(name string) Info {
	now := l.cfg.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.m[name]
	if e == nil {
		return Info{}
	}
	st := Online
	if e.offline || now.Sub(e.lastSeen) >= l.cfg.OfflineAfter {
		st = Offline
	}
	return Info{State: st, LastSeen: e.lastSeen, Endpoint: e.endpoint, Version: e.version}
}
