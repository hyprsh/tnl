package edge

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/hyprsh/tnl/internal/remind"
	"github.com/hyprsh/tnl/internal/state"
)

// waitingLine is a declared line that waits.
type waitingLine struct{ outpost, hostname string }

// waitingWatch logs the declared lines that wait: a warn when one starts to
// wait and a reminder on the shared schedule until it applies or is dropped.
// The applying is logged where it happens, in the store's change path.
type waitingWatch struct {
	log   *slog.Logger
	clock remind.Clock

	mu      sync.Mutex
	tracker *remind.Tracker
	waiting map[string]waitingLine // by waitKey
}

func newWaitingWatch(log *slog.Logger, sched remind.Schedule, clock remind.Clock) *waitingWatch {
	return &waitingWatch{log: log, clock: clock, tracker: remind.NewTracker(sched, clock), waiting: map[string]waitingLine{}}
}

func waitKey(outpost, host string) string { return outpost + "/" + host }

// sync follows the state: it warns for each line that started waiting and
// closes those that no longer wait. It is quick, so it can be a store
// subscriber.
func (w *waitingWatch) sync(st *state.State) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := map[string]waitingLine{}
	for _, d := range st.Declarations {
		for _, l := range d.Lines {
			reason := st.WaitingReason(d.Outpost, l.Hostname)
			if reason == "" {
				continue
			}
			k := waitKey(d.Outpost, l.Hostname)
			now[k] = waitingLine{d.Outpost, l.Hostname}
			if w.tracker.Begin(k) {
				w.log.Warn("declared line waits", "outpost", d.Outpost, "hostname", l.Hostname, "reason", reason)
			}
		}
	}
	for k := range w.waiting {
		if _, still := now[k]; !still {
			w.tracker.End(k)
		}
	}
	w.waiting = now
}

// remind logs the reminders that are owed.
func (w *waitingWatch) remind(st *state.State) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range w.tracker.Due() {
		o := w.waiting[r.Key]
		w.log.Warn("declared line still waits", "outpost", o.outpost, "hostname", o.hostname, "reason", st.WaitingReason(o.outpost, o.hostname), "waiting_for", r.Since.Round(time.Second))
	}
}

// run ticks the reminders until ctx ends.
func (w *waitingWatch) run(ctx context.Context, store *state.Store, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.remind(store.Snapshot())
		}
	}
}
