package logx

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"time"
)

// The ring's bounds: the last RingLines lines or RingBytes of them, whichever
// is smaller.
const (
	RingLines = 1000
	RingBytes = 1 << 20
)

// followBuffer is how many lines a follower may fall behind before the ring
// drops lines for it instead of blocking the logger.
const followBuffer = 256

// Entry is one line of the process's own log, rendered both ways.
type Entry struct {
	Time  time.Time
	Level slog.Level
	Text  string // key=value form, with the timestamp, no newline
	JSON  string // JSON form, no newline
}

// Line is the entry in JSON or text form.
func (e Entry) Line(json bool) string {
	if json {
		return e.JSON
	}
	return e.Text
}

// Ring is the bounded in-memory copy of the process's own log lines that
// `tnl logs` reads. Every line the logger writes at its level goes in, except
// access-log lines.
type Ring struct {
	json bool // the process logs JSON

	mu      sync.Mutex
	entries []Entry
	size    int // bytes of the process-format lines held
	subs    map[chan Entry]struct{}
}

// JSON reports whether the process logs JSON, so its lines are in that form.
func (r *Ring) JSON() bool { return r.json }

func (r *Ring) add(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	r.size += len(e.Line(r.json))
	for len(r.entries) > RingLines || (r.size > RingBytes && len(r.entries) > 1) {
		r.size -= len(r.entries[0].Line(r.json))
		r.entries[0] = Entry{}
		r.entries = r.entries[1:]
	}
	if cap(r.entries) > 2*RingLines+16 {
		r.entries = append(make([]Entry, 0, RingLines+16), r.entries...)
	}
	for ch := range r.subs {
		select {
		case ch <- e:
		default: // a slow follower loses lines; logging never waits
		}
	}
}

// Snapshot returns the lines held, oldest first.
func (r *Ring) Snapshot() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Entry(nil), r.entries...)
}

// Follow returns the lines held and a channel of the lines logged after them,
// with no gap between. The channel drops lines if the reader falls behind.
// stop ends the subscription.
func (r *Ring) Follow() (snapshot []Entry, lines <-chan Entry, stop func()) {
	ch := make(chan Entry, followBuffer)
	r.mu.Lock()
	snapshot = append([]Entry(nil), r.entries...)
	if r.subs == nil {
		r.subs = map[chan Entry]struct{}{}
	}
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	return snapshot, ch, func() {
		r.mu.Lock()
		delete(r.subs, ch)
		r.mu.Unlock()
	}
}

type accessKey struct{}

// AccessContext marks a log call as an access-log line: it goes to stderr as
// usual and stays out of the ring, where its volume would push out the lines
// an operator is looking for.
func AccessContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, accessKey{}, true)
}

// RingOf returns the ring behind a logger built by New, or nil for any other.
func RingOf(lg *slog.Logger) *Ring {
	if lg == nil {
		return nil
	}
	if h, ok := lg.Handler().(*ringHandler); ok {
		return h.ring
	}
	return nil
}

// ringHandler sends each record to the real handler and, rendered with its
// timestamp in both forms, to the ring.
type ringHandler struct {
	next       slog.Handler
	text, json slog.Handler // render into tbuf and jbuf
	tbuf, jbuf *bytes.Buffer
	ring       *Ring
	mu         *sync.Mutex // guards tbuf and jbuf
}

func newRingHandler(next slog.Handler, ring *Ring) *ringHandler {
	h := &ringHandler{next: next, ring: ring, tbuf: &bytes.Buffer{}, jbuf: &bytes.Buffer{}, mu: &sync.Mutex{}}
	ho := &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: lowerLevel}
	h.text = slog.NewTextHandler(h.tbuf, ho)
	h.json = slog.NewJSONHandler(h.jbuf, ho)
	return h
}

func (h *ringHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *ringHandler) Handle(ctx context.Context, r slog.Record) error {
	if access, _ := ctx.Value(accessKey{}).(bool); !access {
		h.mu.Lock()
		h.tbuf.Reset()
		h.jbuf.Reset()
		h.text.Handle(ctx, r)
		h.json.Handle(ctx, r)
		e := Entry{Time: r.Time, Level: r.Level,
			Text: string(bytes.TrimRight(h.tbuf.Bytes(), "\n")),
			JSON: string(bytes.TrimRight(h.jbuf.Bytes(), "\n"))}
		h.mu.Unlock()
		h.ring.add(e)
	}
	return h.next.Handle(ctx, r)
}

func (h *ringHandler) WithAttrs(as []slog.Attr) slog.Handler {
	if len(as) == 0 {
		return h
	}
	return h.derive(func(i slog.Handler) slog.Handler { return i.WithAttrs(as) })
}

func (h *ringHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.derive(func(i slog.Handler) slog.Handler { return i.WithGroup(name) })
}

func (h *ringHandler) derive(op func(slog.Handler) slog.Handler) slog.Handler {
	c := *h
	c.next, c.text, c.json = op(h.next), op(h.text), op(h.json)
	return &c
}
