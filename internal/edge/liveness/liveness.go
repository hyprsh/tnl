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

	ver "github.com/hyprsh/tnl/internal/version"
)

// DefaultOfflineAfter is the silence after which an Outpost is Offline.
const DefaultOfflineAfter = 60 * time.Second

// DefaultSilentAfter is the silence after which the Edge closes an Outpost's
// Tunnel connections: two missed 10 s pings. The Outpost is not Offline yet.
const DefaultSilentAfter = 20 * time.Second

// State is what the Edge thinks of an Outpost.
type State int

const (
	// Unknown is an Outpost never heard from and not seeded: not joined yet.
	Unknown State = iota
	Online
	Offline
	// Incompatible is an Outpost heard from lately whose version is outside the
	// Edge's window.
	Incompatible
	// NeverConnected is an Outpost the Edge has no record of ever hearing from
	// and has not heard from since it started, within the Offline window of the
	// Edge's start. It is not online. Past the window it is Offline.
	NeverConnected
)

func (s State) String() string {
	switch s {
	case Incompatible:
		return "incompatible"
	case Online:
		return "online"
	case Offline:
		return "offline"
	case NeverConnected:
		return "never connected"
	}
	return "unknown"
}

// Config tunes a Liveness. The zero value of each field is its default.
type Config struct {
	OfflineAfter time.Duration // 60 s
	// SilentAfter is the silence after which OnSilent runs: 20 s. At or past
	// OfflineAfter it never does.
	SilentAfter time.Duration
	// EdgeVersion is the Edge's own version, which the window is judged from.
	// Empty judges nothing.
	EdgeVersion string
	Now         func() time.Time // the clock; time.Now
	Log         *slog.Logger     // transitions; nil logs nothing
	// OnOffline runs, with no lock held, once each time an Outpost goes Offline.
	OnOffline func(name string)
	// OnSilent runs, with no lock held, once per silence: at the first check
	// after the Outpost has not been heard from for SilentAfter, if it is not
	// Offline. It returns whether it closed connections, which is logged; false
	// if the Outpost had none. Hearing from the Outpost re-arms it. The
	// Outpost's state does not change.
	OnSilent func(name string) bool
}

// Info is a read of one Outpost.
type Info struct {
	State    State
	LastSeen time.Time // when it was last heard; a seeded Outpost's is the Edge's start
	// Heard is false for an Outpost that is only seeded: nothing has arrived
	// from it since the Edge started, so LastSeen is no sign of life. The
	// tables show "–" for it.
	Heard bool
	// Silent is how long ago LastSeen was, by this clock; zero unless Heard.
	Silent   time.Duration
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
	lastSeen     time.Time
	heard        bool // something has arrived; false for a seeded entry
	never        bool // seeded with no sign the Outpost ever connected
	endpoint     netip.AddrPort
	version      string
	warned       string // the version the Incompatible warning was logged for
	offline      bool   // the Offline transition has been taken
	silenceAsked bool   // OnSilent has been asked in the current silence
}

// outside reports whether the version the Outpost reported is outside the
// Edge's window.
func (l *Liveness) outside(version string) bool {
	return l.cfg.EdgeVersion != "" && version != "" && !ver.EdgeAccepts(l.cfg.EdgeVersion, version)
}

// New returns an empty Liveness.
func New(cfg Config) *Liveness {
	if cfg.OfflineAfter == 0 {
		cfg.OfflineAfter = DefaultOfflineAfter
	}
	if cfg.SilentAfter == 0 {
		cfg.SilentAfter = DefaultSilentAfter
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

// SeedNever tracks an Outpost the Edge has no record of ever hearing from, such
// as a declared one that has not connected yet. The Edge's start opens its
// Offline window, but until something arrives it is NeverConnected, not Online.
// It does nothing if the Outpost is tracked already.
func (l *Liveness) SeedNever(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.m[name]; !ok {
		l.m[name] = &entry{lastSeen: l.cfg.Now(), never: true}
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
		e.lastSeen, e.heard = now, true
		l.mu.Unlock()
		return
	}
	wentOffline := l.expire(e, now)
	was := e.lastSeen
	back := e.offline
	e.offline = false
	e.silenceAsked = false
	e.lastSeen, e.heard = now, true
	l.mu.Unlock()
	if wentOffline {
		l.fireOffline(name, was)
	}
	if back {
		l.cfg.Log.Info("outpost back online", "outpost", name, "gone", now.Sub(was))
	}
}

// SetVersion records the version the Outpost's ping reported.
// Outside the window, the Outpost is Incompatible, and the Edge logs that at
// warn with both versions, once per version change.
func (l *Liveness) SetVersion(name, version string) {
	l.mu.Lock()
	e := l.m[name]
	warn := false
	if e != nil {
		e.version = version
		if l.outside(version) {
			warn = e.warned != version
			e.warned = version
		} else {
			e.warned = ""
		}
	}
	l.mu.Unlock()
	if warn {
		l.cfg.Log.Warn("outpost is incompatible", "outpost", name,
			"edge_version", l.cfg.EdgeVersion, "outpost_version", version,
			"error", ver.IncompatibleOutpost(l.cfg.EdgeVersion, version))
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

// Check takes the Offline transition for every Outpost not heard from for
// OfflineAfter, and the silence close for every other one not heard from for
// SilentAfter: it asks OnSilent once per silence, and the silence ends when the
// Outpost is heard.
func (l *Liveness) Check() {
	now := l.cfg.Now()
	type outpostAt struct {
		name string
		last time.Time // when it was last heard
	}
	var offline, silenceCloses []outpostAt
	l.mu.Lock()
	for name, e := range l.m {
		switch {
		case l.expire(e, now):
			offline = append(offline, outpostAt{name, e.lastSeen})
		case !e.offline && !e.silenceAsked && now.Sub(e.lastSeen) >= l.cfg.SilentAfter:
			silenceCloses = append(silenceCloses, outpostAt{name, e.lastSeen})
		}
	}
	l.mu.Unlock()
	for _, g := range offline {
		l.fireOffline(g.name, g.last)
	}
	for _, c := range silenceCloses {
		if l.cfg.OnSilent == nil || !l.askSilenceClose(c.name, c.last) {
			continue
		}
		l.cfg.Log.Info("outpost silent, closing its connections", "outpost", c.name, "silent_for", l.cfg.SilentAfter)
	}
}

// askSilenceClose runs OnSilent for the Outpost, unless it was heard since
// lastSeen, which ended that silence. It marks the silence asked before it
// runs, so the close is asked for once, and reports whether OnSilent closed
// connections.
func (l *Liveness) askSilenceClose(name string, lastSeen time.Time) bool {
	l.mu.Lock()
	e := l.m[name]
	if e == nil || !e.lastSeen.Equal(lastSeen) || e.offline || e.silenceAsked {
		l.mu.Unlock()
		return false
	}
	e.silenceAsked = true
	l.mu.Unlock()
	return l.cfg.OnSilent(name)
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
	} else if e.never && !e.heard {
		st = NeverConnected
	} else if l.outside(e.version) {
		st = Incompatible
	}
	info := Info{State: st, LastSeen: e.lastSeen, Heard: e.heard, Endpoint: e.endpoint, Version: e.version}
	if e.heard {
		info.Silent = max(now.Sub(e.lastSeen), 0)
	}
	return info
}
