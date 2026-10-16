package state

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/hyprsh/tnl/internal/declare"
)

// DeclaredLine is one line of an Outpost's Declaration with the time it was
// first declared. Whether it is a Route or waits is not stored: it is a Route
// when Routes holds its Hostname through the Outpost.
type DeclaredLine struct {
	declare.Line
	Declared time.Time
}

// Declaration is an Outpost's declared Routes, sorted by Hostname. An Outpost
// with no lines has no Declaration in the state; it is on the CLI.
type Declaration struct {
	Outpost string
	Lines   []DeclaredLine
}

// Declares reports whether the Outpost's Declaration isn't empty: it is then
// the only source of the Outpost's Routes.
func (s *State) Declares(outpost string) bool {
	return len(s.DeclarationOf(outpost)) > 0
}

// DeclarationOf returns the Outpost's lines, nil when it declares none.
func (s *State) DeclarationOf(outpost string) []DeclaredLine {
	for _, d := range s.Declarations {
		if d.Outpost == outpost {
			return d.Lines
		}
	}
	return nil
}

// RemoveDeclaration drops the Outpost's Declaration, if it has one.
func (s *State) RemoveDeclaration(outpost string) {
	s.Declarations = slices.DeleteFunc(slices.Clone(s.Declarations), func(d Declaration) bool { return d.Outpost == outpost })
}

// Route returns the Route for the Hostname.
func (s *State) Route(host string) (Route, bool) {
	for _, r := range s.Routes {
		if r.Hostname == host {
			return r, true
		}
	}
	return Route{}, false
}

// WaitingReason says why the Outpost's line for host is not a Route, or ""
// when it is one.
func (s *State) WaitingReason(outpost, host string) string {
	if r, held := s.Route(host); held {
		if r.Outpost == outpost {
			return ""
		}
		return "held by Outpost " + r.Outpost
	}
	if !s.InZone(host) {
		return "outside every Zone"
	}
	// Free and inside a Zone: it only waits for the change that makes it a Route.
	return "not applied yet"
}

// Entries are the Outpost's Routes and waiting lines as the Edge has them,
// sorted by Hostname.
func (s *State) Entries(outpost string) []declare.Entry {
	var out []declare.Entry
	for _, r := range s.Routes {
		if r.Outpost == outpost {
			out = append(out, declare.Entry{Line: lineOf(r)})
		}
	}
	for _, l := range s.DeclarationOf(outpost) {
		if r, held := s.Route(l.Hostname); !held || r.Outpost != outpost {
			out = append(out, declare.Entry{Line: l.Line, Waiting: true})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hostname < out[j].Hostname })
	return out
}

// DeclarationDigest is the digest of the Outpost's stored Declaration.
func (s *State) DeclarationDigest(outpost string) [declare.DigestSize]byte {
	lines := s.DeclarationOf(outpost)
	ls := make([]declare.Line, len(lines))
	for i, l := range lines {
		ls[i] = l.Line
	}
	return declare.Digest(ls)
}

func lineOf(r Route) declare.Line {
	return declare.Line{Hostname: r.Hostname, Service: r.Service, HostHeader: r.HostHeader, InsecureSkipVerify: r.InsecureSkipVerify}
}

func routeOf(outpost string, l declare.Line) Route {
	return Route{Hostname: l.Hostname, Outpost: outpost, Service: l.Service, HostHeader: l.HostHeader, InsecureSkipVerify: l.InsecureSkipVerify}
}

// WaitingLine is a line that is not a Route, with why.
type WaitingLine struct {
	Hostname string
	Reason   string
}

// DeclarationResult is what ApplyDeclaration did, by Hostname.
type DeclarationResult struct {
	Added     []string // lines that became Routes
	Changed   []string // lines whose Route was edited, an option or the Service
	Unchanged []string // lines whose Route was left alone
	Removed   []string // lines the Declaration no longer has
	Switched  []string // Routes made with the CLI that the Declaration replaced
	Waiting   []WaitingLine
	// Close holds the Hostnames whose connections end: changed, removed and
	// switched Routes.
	Close []string
	// Dial holds the Routes to test-dial: new ones and those whose Service changed.
	Dial []Route
}

// ApplyDeclaration replaces the Outpost's Declaration with lines, as one diff
// by Hostname: an unchanged line keeps its Route, a changed line edits it, and
// a line that is gone removes it. A line whose Hostname another Outpost holds,
// or that is outside every Zone, is stored and waits. An empty lines hands the
// Outpost back to the CLI. The first Declaration from an Outpost with CLI Routes
// replaces them. lines must be valid, with no Hostname twice.
func (s *State) ApplyDeclaration(outpost string, lines []declare.Line, now time.Time) DeclarationResult {
	var res DeclarationResult
	old := map[string]DeclaredLine{}
	for _, l := range s.DeclarationOf(outpost) {
		old[l.Hostname] = l
	}
	if len(lines) == 0 && len(old) == 0 {
		return res // an Outpost on the CLI stays there
	}
	keep := map[string]bool{}
	for _, l := range lines {
		keep[l.Hostname] = true
	}

	// Routes through the Outpost that no line asks for are gone.
	for _, r := range s.Routes {
		if r.Outpost != outpost || keep[r.Hostname] {
			continue
		}
		if _, was := old[r.Hostname]; !was {
			res.Switched = append(res.Switched, r.Hostname)
		}
		res.Close = append(res.Close, r.Hostname)
	}
	s.Routes = slices.DeleteFunc(slices.Clone(s.Routes), func(r Route) bool {
		return r.Outpost == outpost && !keep[r.Hostname]
	})
	for host := range old {
		if !keep[host] {
			res.Removed = append(res.Removed, host)
		}
	}

	stored := make([]DeclaredLine, 0, len(lines))
	for _, l := range lines {
		first := now
		if o, was := old[l.Hostname]; was {
			first = o.Declared
		}
		stored = append(stored, DeclaredLine{Line: l, Declared: first})
		want := routeOf(outpost, l)
		have, held := s.Route(l.Hostname)
		switch {
		case held && have.Outpost != outpost:
			res.Waiting = append(res.Waiting, WaitingLine{l.Hostname, s.WaitingReason(outpost, l.Hostname)})
		case held && have == want:
			res.Unchanged = append(res.Unchanged, l.Hostname)
		case held:
			for i := range s.Routes {
				if s.Routes[i].Hostname == l.Hostname {
					s.Routes[i] = want
				}
			}
			res.Changed = append(res.Changed, l.Hostname)
			res.Close = append(res.Close, l.Hostname)
			if have.Service != want.Service {
				res.Dial = append(res.Dial, want)
			}
		case !s.InZone(l.Hostname):
			res.Waiting = append(res.Waiting, WaitingLine{l.Hostname, "outside every Zone"})
		default:
			s.Routes = append(s.Routes, want)
			res.Added = append(res.Added, l.Hostname)
			res.Dial = append(res.Dial, want)
		}
	}
	sort.Slice(stored, func(i, j int) bool { return stored[i].Hostname < stored[j].Hostname })
	s.Declarations = slices.DeleteFunc(slices.Clone(s.Declarations), func(d Declaration) bool { return d.Outpost == outpost })
	if len(stored) > 0 {
		s.Declarations = append(s.Declarations, Declaration{Outpost: outpost, Lines: stored})
		sort.Slice(s.Declarations, func(i, j int) bool { return s.Declarations[i].Outpost < s.Declarations[j].Outpost })
	}
	for _, list := range [][]string{res.Added, res.Changed, res.Unchanged, res.Removed, res.Switched, res.Close} {
		sort.Strings(list)
	}
	return res
}

// validateDeclarations checks the Declarations against the Outposts and
// Routes. Every Route of a declaring Outpost must be one of its lines.
func (s *State) validateDeclarations(outposts map[string]bool) error {
	seen := map[string]bool{}
	for _, d := range s.Declarations {
		e := fmt.Sprintf("declaration of outpost %q", d.Outpost)
		if !outposts[d.Outpost] {
			return invalid(e, "outpost %q does not exist", d.Outpost)
		}
		if seen[d.Outpost] {
			return invalid(e, "listed twice")
		}
		seen[d.Outpost] = true
		if len(d.Lines) == 0 {
			return invalid(e, "has no lines; an Outpost on the CLI has no Declaration")
		}
		hosts := map[string]bool{}
		for _, l := range d.Lines {
			le := fmt.Sprintf("%s, line %q", e, l.Hostname)
			if l.Hostname == "" {
				return invalid(e, "a line has no Hostname")
			}
			if hosts[l.Hostname] {
				return invalid(le, "Hostname listed twice")
			}
			hosts[l.Hostname] = true
			if l.Declared.IsZero() {
				return invalid(le, "no time first declared")
			}
			if _, err := ParseService(l.Service); err != nil {
				return invalid(le, "%v", err)
			}
			if err := CheckHostHeader(l.HostHeader); err != nil {
				return invalid(le, "%v", err)
			}
		}
		for _, r := range s.Routes {
			if r.Outpost == d.Outpost && !hosts[r.Hostname] {
				return invalid(fmt.Sprintf("route %q", r.Hostname), "outpost %q declares its Routes, and this is not one of its lines", d.Outpost)
			}
		}
	}
	return nil
}
