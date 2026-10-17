package state

import "sort"

// Handed is a waiting line that became a Route in the change that freed its
// Hostname or added its Zone.
type Handed struct {
	Outpost string
	Route   Route
}

// Handover turns every waiting line whose Hostname is free and inside a Zone
// into a Route, and returns them. Where several Outposts wait for one Hostname,
// the line declared first gets it, then the Outpost's name decides. The store
// runs it in every change, inside the lock, so no other change can take a
// Hostname between the change that frees it and the line that gets it.
func (s *State) Handover() []Handed {
	type waiting struct {
		outpost string
		line    DeclaredLine
	}
	var queue []waiting
	for _, d := range s.Declarations {
		for _, l := range d.Lines {
			if _, held := s.Route(l.Hostname); !held && s.InZone(l.Hostname) {
				queue = append(queue, waiting{d.Outpost, l})
			}
		}
	}
	if len(queue) == 0 {
		return nil
	}
	sort.SliceStable(queue, func(i, j int) bool {
		a, b := queue[i], queue[j]
		switch {
		case !a.line.Declared.Equal(b.line.Declared):
			return a.line.Declared.Before(b.line.Declared)
		case a.outpost != b.outpost:
			return a.outpost < b.outpost
		}
		return a.line.Hostname < b.line.Hostname
	})
	var out []Handed
	routes := append([]Route(nil), s.Routes...)
	taken := map[string]bool{}
	for _, w := range queue {
		if taken[w.line.Hostname] {
			continue
		}
		taken[w.line.Hostname] = true
		r := RouteOf(w.outpost, w.line.Line)
		routes = append(routes, r)
		out = append(out, Handed{Outpost: w.outpost, Route: r})
	}
	s.Routes = routes
	return out
}
