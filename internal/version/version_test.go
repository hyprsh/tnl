package version

import "testing"

func TestEdgeAcceptsOutpost(t *testing.T) {
	cases := []struct {
		edge, outpost string
		want          bool
	}{
		// Before 1.0: only the same minor, whichever side is newer.
		{"0.3.4", "0.3.1", true},
		{"0.3.1", "0.3.4", true},
		{"0.3.4", "0.4.0", false},
		{"0.4.0", "0.3.9", false},
		{"v0.3.4", "0.3.1", true}, // a leading v is the release tag's
		{"0.3.4-rc.1", "0.3.0", true},
		// 1.x: its own major whichever side is newer, plus the previous major.
		{"1.4.2", "1.4.2", true},
		{"1.4.2", "1.9.0", true},
		{"1.4.2", "1.0.0", true},
		{"1.4.2", "2.0.0", false},
		{"1.4.2", "0.9.1", true},
		// 2.x accepts 1.x but not 0.x.
		{"2.0.0", "2.3.1", true},
		{"2.0.0", "1.8.0", true},
		{"2.0.0", "0.9.1", false},
		{"2.0.0", "3.0.0", false},
		// A version that isn't SemVer (a dev build) is not judged.
		{"1.4.2", "dev", true},
		{"dev", "0.4.0", true},
	}
	for _, c := range cases {
		if got := EdgeAccepts(c.edge, c.outpost); got != c.want {
			t.Errorf("EdgeAccepts(%q, %q) = %v, want %v", c.edge, c.outpost, got, c.want)
		}
	}
}

func TestOutpostAcceptsEdge(t *testing.T) {
	cases := []struct {
		outpost, edge string
		want          bool
	}{
		{"1.4.2", "1.4.2", true},
		{"1.4.2", "1.3.0", true},  // same major, older: the Edge is the judge
		{"1.4.2", "2.0.0", true},  // newer major
		{"2.0.0", "1.9.0", false}, // older major
		{"0.4.0", "0.3.0", true},  // before 1.0 the Outpost leaves it to the Edge
		{"1.0.0", "0.9.0", false},
		{"1.0.0", "dev", true},
	}
	for _, c := range cases {
		if got := OutpostAcceptsEdge(c.outpost, c.edge); got != c.want {
			t.Errorf("OutpostAcceptsEdge(%q, %q) = %v, want %v", c.outpost, c.edge, got, c.want)
		}
	}
}

func TestCompatibilityLine(t *testing.T) {
	cases := []struct {
		version, build, want string
	}{
		{"0.3.1", "full", "works with Edges and Outposts 0.3.x"},
		{"0.3.1", "outpost", "works with Edges and Outposts 0.3.x"},
		{"1.4.2", "full", "works with Edges and Outposts 1.x"},
		{"2.0.0", "full", "accepts Outposts 1.x and 2.x"},
		{"2.0.0", "outpost", "works with Edges and Outposts 2.x"},
		{"dev", "full", "works with any version (a development build)"},
	}
	for _, c := range cases {
		if got := CompatibilityLine(c.version, c.build); got != c.want {
			t.Errorf("CompatibilityLine(%q, %q) = %q, want %q", c.version, c.build, got, c.want)
		}
	}
}

func TestRefusalMessages(t *testing.T) {
	// The Outpost is older than the window: upgrade the Outpost.
	if got, want := IncompatibleOutpost("2.0.0", "0.9.1"), "the Edge (2.0.0) doesn't accept this Outpost (0.9.1); upgrade the Outpost"; got != want {
		t.Errorf("got %q", got)
	}
	// The Outpost is newer than the window: upgrade the Edge.
	if got, want := IncompatibleOutpost("0.3.4", "0.4.0"), "the Edge (0.3.4) doesn't accept this Outpost (0.4.0); upgrade the Edge"; got != want {
		t.Errorf("got %q", got)
	}
	if got, want := UnknownField("1.4.2", "--new-opt", "1.5.0"), "the Edge (1.4.2) doesn't support `--new-opt`; it needs 1.5 or newer; upgrade the Edge"; got != want {
		t.Errorf("got %q", got)
	}
	if got, want := UnknownField("1.4.2", "--new-opt", ""), "the Edge (1.4.2) doesn't support `--new-opt`; it needs a newer version; upgrade the Edge"; got != want {
		t.Errorf("got %q", got)
	}
	if got, want := EdgeTooOld("2.0.0", "1.4.2"), "this Outpost (2.0.0) is a newer major than the Edge (1.4.2) and can't work with it; it keeps trying; upgrade the Edge to 2.x or newer, or install Outpost 1.x"; got != want {
		t.Errorf("got %q", got)
	}
}
