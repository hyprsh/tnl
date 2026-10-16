package edge

import "sync/atomic"

// late is a value that exists only once the web side is built, after the API
// server that uses it. Until set, get returns the default it is given.
type late[T any] struct{ p atomic.Pointer[T] }

func (l *late[T]) set(v T) { l.p.Store(&v) }

func (l *late[T]) clear() { l.p.Store(nil) }

func (l *late[T]) get(def T) T {
	if v := l.p.Load(); v != nil {
		return *v
	}
	return def
}
