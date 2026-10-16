// Package settings reads the TNL_* surface of the startup commands, for both
// `tnl connect` and `tnl edge`: the unknown-variable check, secrets given as a
// file or a variable, and the declared lines: an Outpost's Route lines, and
// the Edge's Zone and Outpost lines. It imports nothing outside the standard
// library and the shared checks, so the Outpost-only build can use it.
package settings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hyprsh/tnl/internal/declare"
	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/state"
)

// RoutePrefix starts every route-line variable.
const RoutePrefix = "TNL_ROUTE_"

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

type declaredLine struct{ name, text string }

// collect gathers the lines of one kind: the variables with the prefix, sorted
// by name, then the repeated flag's lines. Setting both forms is refused.
func collect(environ, flagLines []string, prefix, flagName string) ([]declaredLine, error) {
	var lines []declaredLine
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if len(flagLines) > 0 {
			return nil, fmt.Errorf("%s and %s are both set: declare them one way", flagName, name)
		}
		if err := checkLabel(prefix, strings.TrimPrefix(name, prefix)); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		lines = append(lines, declaredLine{name, value})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].name < lines[j].name })
	for i, f := range flagLines {
		// A flag line is named by its first word, unless that is an option; the
		// text itself is never echoed, since it may hold a value.
		name := fmt.Sprintf("%s #%d", flagName, i+1)
		if first := strings.Fields(f); len(first) > 0 && len(first[0]) <= 63 && dnsname.CheckName(dnsname.Normalize(first[0])) == nil {
			name = flagName + " " + first[0]
		}
		lines = append(lines, declaredLine{name, f})
	}
	return lines, nil
}

// ParseRoutes parses the TNL_ROUTE_* variables in environ and the arguments of
// the repeated --route flag into the Declaration, sorted by Hostname; both
// forms of a line parse to the same line. Setting both forms is refused. Every
// error names the variable, or the --route line. labels names each Hostname's
// line: the variable that declared it, or the --route line. The Outpost's log
// names a line by it; the Edge never sees it.
func ParseRoutes(environ, flagLines []string) (lines []declare.Line, labels map[string]string, err error) {
	collected, err := collect(environ, flagLines, RoutePrefix, "--route")
	if err != nil {
		return nil, nil, err
	}
	labels = map[string]string{}
	for _, l := range collected {
		r, err := parseRoute(l.text)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", l.name, err)
		}
		if other, dup := labels[r.Hostname]; dup {
			return nil, nil, fmt.Errorf("%s: %s is also on %s", l.name, r.Hostname, other)
		}
		labels[r.Hostname] = l.name
		lines = append(lines, r)
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].Hostname < lines[j].Hostname })
	return lines, labels, nil
}

func checkLabel(prefix, label string) error {
	if label == "" {
		return fmt.Errorf("the variable needs a label after %s", prefix)
	}
	for _, c := range label {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return fmt.Errorf("the label %q may hold only letters, digits and _", label)
		}
	}
	return nil
}

func parseRoute(text string) (declare.Line, error) {
	words := strings.Fields(text)
	if len(words) < 2 {
		return declare.Line{}, fmt.Errorf("want <hostname> <service-url> [--host-header V] [--insecure-skip-verify]")
	}
	var r declare.Line
	host := dnsname.Normalize(words[0])
	if err := dnsname.CheckName(host); err != nil {
		return declare.Line{}, fmt.Errorf("invalid Hostname %q: %v", words[0], err)
	}
	r.Hostname = host
	svc, err := state.ParseService(words[1])
	if err != nil {
		return declare.Line{}, err
	}
	r.Service = svc.Canonical
	var gotHeader, gotInsecure bool
	for rest := words[2:]; len(rest) > 0; rest = rest[1:] {
		switch w := rest[0]; w {
		case "--host-header":
			if len(rest) < 2 {
				return declare.Line{}, fmt.Errorf("--host-header needs a value")
			}
			if gotHeader {
				return declare.Line{}, fmt.Errorf("--host-header is given twice")
			}
			if err := state.CheckHostHeader(rest[1]); err != nil {
				return declare.Line{}, err
			}
			gotHeader, r.HostHeader = true, rest[1]
			rest = rest[1:]
		case "--insecure-skip-verify":
			if gotInsecure {
				return declare.Line{}, fmt.Errorf("--insecure-skip-verify is given twice")
			}
			gotInsecure, r.InsecureSkipVerify = true, true
		default:
			if strings.HasPrefix(w, "-") {
				return declare.Line{}, fmt.Errorf("unknown option %s", w)
			}
			return declare.Line{}, fmt.Errorf("unexpected word %q after the Service URL", w)
		}
	}
	return r, nil
}
