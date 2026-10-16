// Package version is the one place tnl judges versions: the window an Edge
// accepts Outposts in, what an Outpost accepts of an Edge, the compatibility
// line of `tnl version` and the wording of every version-aware refusal
// (docs/spec/v1.md, Versions; ADR 0008). It imports nothing, so both roles
// link it.
package version

import (
	"fmt"
	"strconv"
	"strings"
)

// V is a parsed SemVer version. Pre-release and build parts are dropped: the
// window looks at major and minor only.
type V struct{ Major, Minor, Patch int }

// Parse reads MAJOR.MINOR.PATCH, with an optional leading "v" and an optional
// -pre-release or +build suffix. A version that is not SemVer, such as "dev",
// gives ok false.
func Parse(s string) (v V, ok bool) {
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return V{}, false
	}
	var n [3]int
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 || (len(p) > 1 && p[0] == '0') {
			return V{}, false
		}
		n[i] = x
	}
	return V{n[0], n[1], n[2]}, true
}

// EdgeAccepts is the version window: whether an Edge of version edge accepts an
// Outpost of version outpost.
//
// Before 1.0, two versions work together only when they share the minor. From
// 1.0, an Edge accepts its own major whichever side is newer, plus the
// previous major. A version that isn't SemVer (a development build) is never
// judged, on either side.
func EdgeAccepts(edge, outpost string) bool {
	e, eok := Parse(edge)
	o, ook := Parse(outpost)
	if !eok || !ook {
		return true
	}
	if e.Major == 0 {
		return o.Major == 0 && o.Minor == e.Minor
	}
	return o.Major == e.Major || o.Major == e.Major-1
}

// OutpostAcceptsEdge is what an Outpost checks: it refuses only an Edge of an
// older major, because only the Edge knows its own window.
func OutpostAcceptsEdge(outpost, edge string) bool {
	o, ook := Parse(outpost)
	e, eok := Parse(edge)
	if !eok || !ook {
		return true
	}
	return e.Major >= o.Major
}

// CompatibilityLine is the line `tnl version` prints under the version and
// build. A full build at major 2 or later is an Edge that accepts the previous
// major too; every other build works with its own line.
func CompatibilityLine(version, build string) string {
	v, ok := Parse(version)
	switch {
	case !ok:
		return "works with any version (a development build)"
	case v.Major == 0:
		return fmt.Sprintf("works with Edges and Outposts 0.%d.x", v.Minor)
	case v.Major >= 2 && build == "full":
		return fmt.Sprintf("accepts Outposts %d.x and %d.x", v.Major-1, v.Major)
	}
	return fmt.Sprintf("works with Edges and Outposts %d.x", v.Major)
}

// OutpostIsNewer says which side of the window an Outpost the Edge doesn't
// accept is on, so the refusal names the side to upgrade.
func outpostIsNewer(edge, outpost string) bool {
	e, eok := Parse(edge)
	o, ook := Parse(outpost)
	if !eok || !ook {
		return false
	}
	if o.Major != e.Major {
		return o.Major > e.Major
	}
	return o.Minor > e.Minor
}

// IncompatibleOutpost is the refusal of an API call from an Outpost outside
// the window, and the Edge's warn line about it.
func IncompatibleOutpost(edge, outpost string) string {
	side := "Outpost"
	if outpostIsNewer(edge, outpost) {
		side = "Edge"
	}
	return fmt.Sprintf("the Edge (%s) doesn't accept this Outpost (%s); upgrade the %s", edge, outpost, side)
}

// UnknownField is the refusal of a request with a field the Edge doesn't know.
// what names the field the way the caller knows it, such as `--new-opt`; since
// is the version that added it, which only the newer side knows, or empty.
func UnknownField(edge, what, since string) string {
	need := "a newer version"
	if v, ok := Parse(since); ok {
		need = fmt.Sprintf("%d.%d", v.Major, v.Minor)
		if v.Patch != 0 {
			need = fmt.Sprintf("%s.%d", need, v.Patch)
		}
		need += " or newer"
	}
	return fmt.Sprintf("the Edge (%s) doesn't support `%s`; it needs %s; upgrade the Edge", edge, what, need)
}

// EdgeTooOld is why an Outpost can't work with an Edge of an older major, and what to
// do about it.
func EdgeTooOld(outpost, edge string) string {
	o, ook := Parse(outpost)
	e, eok := Parse(edge)
	if !ook || !eok {
		return fmt.Sprintf("this Outpost (%s) is newer than the Edge (%s) allows; upgrade the Edge", outpost, edge)
	}
	return fmt.Sprintf("this Outpost (%s) is a newer major than the Edge (%s) and can't work with it; it keeps trying; upgrade the Edge to %d.x or newer, or install Outpost %d.x",
		outpost, edge, o.Major, e.Major)
}

// TokenOutOfWindow is the refusal of a join token made by an Edge that won't
// work with this Outpost, before any state is touched. It names both versions
// and the Outpost version to install: the Edge's own always works.
func TokenOutOfWindow(outpost, edge string) string {
	return fmt.Sprintf("this join token is from an Edge (%s) that doesn't work with this Outpost (%s); install Outpost %s, the Edge's version", edge, outpost, strings.TrimPrefix(edge, "v"))
}
