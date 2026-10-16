package outpost

import (
	"context"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/remind"
	"github.com/hyprsh/tnl/internal/tunnel"
)

// DefaultNoAnswerAfter is the silence after which the Outpost says the Edge
// does not answer and re-resolves its address, and the interval after that.
const DefaultNoAnswerAfter = 30 * time.Second

// watcher follows the Edge's answers to the pings. An Outpost never gives up
// on the Edge, so it only logs and re-resolves, and never ends the Outpost.
//
// A silence under noAnswerAfter is a blip, such as an Edge restart, and logs
// nothing. Past it the Tunnel is down: the watcher warns once, reminds on the
// schedule, and re-resolves the Edge's address every noAnswerAfter. The first
// answer afterwards logs "Tunnel up" with the outage's length.
type watcher struct {
	log           *slog.Logger
	clock         remind.Clock
	tun           *tunnel.Tunnel
	edgeKey       keys.Key
	edgePort      uint16
	edgeAddress   string // --address as given
	resolve       func(ctx context.Context, host string) (netip.Addr, error)
	isName        bool // the address is a name, so it can change
	noAnswerAfter time.Duration
	poll          time.Duration

	reminders *remind.Tracker

	mu          sync.Mutex
	edgeIP      netip.Addr
	edgeVersion string
	up          bool      // a "Tunnel up" has been logged for the current stretch
	lastAnswer  time.Time // the last answer, or the start
	lastResolve time.Time // zero when no re-resolution has run in this outage
}

const reminderKey = "edge"

func newWatcher(cfg Config, tun *tunnel.Tunnel, st *State, edgeIP netip.Addr, resolve func(context.Context, string) (netip.Addr, error)) *watcher {
	clock := cfg.Clock
	if clock == nil {
		clock = remind.SystemClock{}
	}
	after := cfg.NoAnswerAfter
	if after == 0 {
		after = DefaultNoAnswerAfter
	}
	sched := cfg.Reminders
	if sched.Every == 0 {
		sched = remind.Standard
	}
	_, perr := netip.ParseAddr(st.EdgeAddress)
	return &watcher{
		log: cfg.Log, clock: clock, tun: tun, edgeKey: st.EdgeKey, edgePort: uint16(st.EdgePort),
		edgeAddress: st.EdgeAddress, resolve: resolve, isName: perr != nil,
		noAnswerAfter: after, poll: min(time.Second, after/4),
		reminders: remind.NewTracker(sched, clock),
		edgeIP:    edgeIP, lastAnswer: clock.Now(),
	}
}

// answered is called for each of the Edge's answers to a ping.
func (w *watcher) answered(version string) {
	now := w.clock.Now()
	w.mu.Lock()
	outage := now.Sub(w.lastAnswer)
	wasUp := w.up
	w.up = true
	w.lastAnswer, w.lastResolve = now, time.Time{}
	w.edgeVersion = version
	ip := w.edgeIP
	w.mu.Unlock()
	w.reminders.End(reminderKey)
	if !wasUp {
		w.log.Info("Tunnel up", "edge_address", w.edgeAddress, "edge_ip", ip.String(), "edge_version", version, "outage", outage.Round(time.Millisecond))
	}
}

// run checks every poll until ctx ends.
func (w *watcher) run(ctx context.Context) {
	t := time.NewTicker(w.poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.check(ctx)
		}
	}
}

func (w *watcher) check(ctx context.Context) {
	now := w.clock.Now()
	w.mu.Lock()
	silent := now.Sub(w.lastAnswer)
	if silent < w.noAnswerAfter {
		w.mu.Unlock()
		return
	}
	w.up = false
	ip := w.edgeIP
	resolveNow := w.isName && (w.lastResolve.IsZero() || now.Sub(w.lastResolve) >= w.noAnswerAfter)
	if resolveNow {
		w.lastResolve = now
	}
	w.mu.Unlock()

	if resolveNow {
		ip = w.reresolve(ctx, ip)
	}
	fields := []any{"edge_address", w.edgeAddress, "edge_ip", ip.String(), "silent_for", silent.Round(time.Second)}
	const why = "the Edge is down or unreachable, or this Outpost was removed or rekeyed on the Edge"
	if w.reminders.Begin(reminderKey) {
		w.log.Warn("No answer from the Edge", append(fields, "possible_causes", why)...)
		return
	}
	for range w.reminders.Due() {
		w.log.Warn("Still no answer from the Edge", append(fields, "possible_causes", why)...)
	}
}

// reresolve looks the Edge's address up again and switches the Tunnel to the
// new IP if it changed. It returns the IP now in use.
func (w *watcher) reresolve(ctx context.Context, old netip.Addr) netip.Addr {
	ip, err := w.resolve(ctx, w.edgeAddress)
	if err != nil {
		w.log.Debug("could not resolve the Edge's address again", "edge_address", w.edgeAddress, "error", err)
		return old
	}
	w.log.Debug("resolved the Edge's address again", "edge_address", w.edgeAddress, "edge_ip", ip.String())
	if ip == old {
		return old
	}
	if err := w.tun.SetEndpoint(w.edgeKey, netip.AddrPortFrom(ip, w.edgePort)); err != nil {
		w.log.Warn("could not switch to the Edge's new address", "edge_address", w.edgeAddress, "edge_ip", ip.String(), "error", err)
		return old
	}
	w.mu.Lock()
	w.edgeIP = ip
	w.mu.Unlock()
	w.log.Info("the Edge's address changed", "edge_address", w.edgeAddress, "old_ip", old.String(), "edge_ip", ip.String())
	return ip
}
