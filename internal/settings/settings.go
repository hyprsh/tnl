// Package settings reads the TNL_* surface of the startup commands: the
// unknown-variable check and the route lines of an Outpost's Declaration. It
// imports nothing outside the standard library and the shared checks, so the
// Outpost-only build can use it.
package settings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/state"
)

// RoutePrefix starts every route-line variable.
const RoutePrefix = "TNL_ROUTE_"

// Route is one declared Route. The label of its line is not part of it: it
// names the line in messages and never reaches the Edge.
type Route struct {
	Hostname           string
	Service            string // canonical
	HostHeader         string
	InsecureSkipVerify bool
}

// Declaration is an Outpost's declared Routes, sorted by Hostname. Both forms
// of a line parse to the same Declaration.
type Declaration []Route

// CheckEnv refuses a TNL_* variable that is neither in known nor starts with
// one of prefixes. It echoes names only, never values. environ is in
// os.Environ form.
func CheckEnv(environ, known []string, prefixes ...string) error {
	isKnown := make(map[string]bool, len(known))
	for _, k := range known {
		isKnown[k] = true
	}
	var unknown []string
next:
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, "TNL_") || isKnown[name] {
			continue
		}
		for _, p := range prefixes {
			if p != "" && strings.HasPrefix(name, p) {
				continue next
			}
		}
		unknown = append(unknown, name)
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	name := unknown[0]
	best, bestDist := "", 4
	for _, k := range known {
		if d := distance(name, k); d < bestDist {
			best, bestDist = k, d
		}
	}
	if best != "" {
		return fmt.Errorf("unknown variable %s: did you mean %s?", name, best)
	}
	return fmt.Errorf("unknown variable %s", name)
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// ParseRoutes parses the TNL_ROUTE_* variables in environ and the arguments of
// the repeated --route flag. Setting both forms is refused. Every error names
// the variable, or the --route line.
func ParseRoutes(environ, flagLines []string) (Declaration, error) {
	d, _, err := ParseRoutesLabelled(environ, flagLines)
	return d, err
}

// ParseRoutesLabelled is ParseRoutes that also returns each Hostname's label:
// the variable that declared it, or the --route text. The Outpost's log names
// a line by it; the Edge never sees it.
func ParseRoutesLabelled(environ, flagLines []string) (Declaration, map[string]string, error) {
	type line struct{ name, text string }
	var lines []line
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, RoutePrefix) {
			continue
		}
		if len(flagLines) > 0 {
			return nil, nil, fmt.Errorf("--route and %s are both set: declare Routes one way", name)
		}
		lines = append(lines, line{name, value})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].name < lines[j].name })
	for _, f := range flagLines {
		lines = append(lines, line{"--route " + f, f})
	}
	var d Declaration
	seen := map[string]string{}
	for _, l := range lines {
		if strings.HasPrefix(l.name, RoutePrefix) {
			if err := checkLabel(strings.TrimPrefix(l.name, RoutePrefix)); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", l.name, err)
			}
		}
		r, err := parseRoute(l.text)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", l.name, err)
		}
		if other, dup := seen[r.Hostname]; dup {
			return nil, nil, fmt.Errorf("%s: %s is also on %s", l.name, r.Hostname, other)
		}
		seen[r.Hostname] = l.name
		d = append(d, r)
	}
	sort.Slice(d, func(i, j int) bool { return d[i].Hostname < d[j].Hostname })
	return d, seen, nil
}

func checkLabel(label string) error {
	if label == "" {
		return fmt.Errorf("the variable needs a label after %s", RoutePrefix)
	}
	for _, c := range label {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return fmt.Errorf("the label %q may hold only letters, digits and _", label)
		}
	}
	return nil
}

func parseRoute(text string) (Route, error) {
	words := strings.Fields(text)
	if len(words) < 2 {
		return Route{}, fmt.Errorf("want <hostname> <service-url> [--host-header V] [--insecure-skip-verify]")
	}
	var r Route
	host := dnsname.Normalize(words[0])
	if err := dnsname.CheckName(host); err != nil {
		return Route{}, fmt.Errorf("invalid Hostname %q: %v", words[0], err)
	}
	r.Hostname = host
	svc, err := state.ParseService(words[1])
	if err != nil {
		return Route{}, err
	}
	r.Service = svc.Canonical
	var gotHeader, gotInsecure bool
	for rest := words[2:]; len(rest) > 0; rest = rest[1:] {
		switch w := rest[0]; w {
		case "--host-header":
			if len(rest) < 2 {
				return Route{}, fmt.Errorf("--host-header needs a value")
			}
			if gotHeader {
				return Route{}, fmt.Errorf("--host-header is given twice")
			}
			if err := state.CheckHostHeader(rest[1]); err != nil {
				return Route{}, err
			}
			gotHeader, r.HostHeader = true, rest[1]
			rest = rest[1:]
		case "--insecure-skip-verify":
			if gotInsecure {
				return Route{}, fmt.Errorf("--insecure-skip-verify is given twice")
			}
			gotInsecure, r.InsecureSkipVerify = true, true
		default:
			if strings.HasPrefix(w, "-") {
				return Route{}, fmt.Errorf("unknown option %s", w)
			}
			return Route{}, fmt.Errorf("unexpected word %q after the Service URL", w)
		}
	}
	return r, nil
}
