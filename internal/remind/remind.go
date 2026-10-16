// Package remind is the one reminder schedule behind "transitions and spaced
// reminders, never every attempt" (spec, Logs). Something that can stay wrong
// for hours is Begun when it starts, Due says when a reminder is owed, and End
// closes it. It is pull-based: nothing here owns a timer, so a test drives time
// with a Clock and a caller ticks as often as it likes. It has no dependencies,
// so the Outpost build links it too.
package remind

import (
	"cmp"
	"slices"
	"sync"
	"time"
)

// Clock tells the time. Tests inject one.
type Clock interface{ Now() time.Time }

// SystemClock is the wall clock.
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Schedule says when reminders fall, as offsets from the start: each of After,
// then one every Every.
type Schedule struct {
	After []time.Duration
	Every time.Duration
}

var (
	// Standard reminds after 5 min and 30 min, then hourly.
	Standard = Schedule{After: []time.Duration{5 * time.Minute, 30 * time.Minute}, Every: time.Hour}
	// Daily is for certificate failures.
	Daily = Schedule{Every: 24 * time.Hour}
)

// next is the offset of the reminder after n have been given.
func (s Schedule) next(n int) time.Duration {
	if n < len(s.After) {
		return s.After[n]
	}
	var last time.Duration
	if len(s.After) > 0 {
		last = s.After[len(s.After)-1]
	}
	return last + time.Duration(n-len(s.After)+1)*s.Every
}

// Tracker follows many things going wrong, each under a key.
type Tracker struct {
	sched Schedule
	clock Clock

	mu    sync.Mutex
	items map[string]*item
}

type item struct {
	start time.Time
	given int
}

// NewTracker follows s on clock; a nil clock is the wall clock.
func NewTracker(s Schedule, clock Clock) *Tracker {
	if clock == nil {
		clock = SystemClock{}
	}
	return &Tracker{sched: s, clock: clock, items: map[string]*item{}}
}

// Begin starts key if it isn't going already, and says whether it was new:
// the caller logs the start only then.
func (t *Tracker) Begin(key string) (started bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.items[key]; ok {
		return false
	}
	t.items[key] = &item{start: t.clock.Now()}
	return true
}

// End closes key and says how long it went on, and whether it was going.
func (t *Tracker) End(key string) (since time.Duration, was bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	it, ok := t.items[key]
	if !ok {
		return 0, false
	}
	delete(t.items, key)
	return t.clock.Now().Sub(it.start), true
}

// Reminder is one owed reminder.
type Reminder struct {
	Key   string
	Since time.Duration // how long it has been going
}

// Due returns the reminders owed now, sorted by key. Each is owed once: a late
// call gives one reminder, not a backlog.
func (t *Tracker) Due() []Reminder {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock.Now()
	var out []Reminder
	for k, it := range t.items {
		since := now.Sub(it.start)
		if since < t.sched.next(it.given) {
			continue
		}
		for since >= t.sched.next(it.given) {
			it.given++
		}
		out = append(out, Reminder{Key: k, Since: since})
	}
	slices.SortFunc(out, func(a, b Reminder) int { return cmp.Compare(a.Key, b.Key) })
	return out
}
