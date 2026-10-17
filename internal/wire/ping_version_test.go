package wire

import "testing"

func TestPingVersionReadsWhatParsePingRefuses(t *testing.T) {
	// A later Outpost's ping: version first, then a tag this build does not know.
	later := []byte{'t', 'n', 'l', 1, 1, 1, 5, 'v', '0', '.', '2', '0', 99, 2, 7, 7, 0}
	if _, err := ParsePing(later); err == nil {
		t.Fatal("ParsePing took a ping with an unknown tag")
	}
	if v, ok := PingVersion(later); !ok || v != "v0.20" {
		t.Errorf("got %q, %v", v, ok)
	}
	// An unknown tag ahead of the version is skipped.
	ahead := []byte{'t', 'n', 'l', 1, 1, 99, 1, 7, 1, 2, 'v', '2', 0}
	if v, ok := PingVersion(ahead); !ok || v != "v2" {
		t.Errorf("unknown tag first: %q, %v", v, ok)
	}
	good, _ := AppendPing(nil, Ping{Version: "v1"})
	if v, ok := PingVersion(good); !ok || v != "v1" {
		t.Errorf("good ping: %q, %v", v, ok)
	}
}

func TestPingVersionRefusesWhatCarriesNoVersion(t *testing.T) {
	for name, b := range map[string][]byte{
		"empty":          nil,
		"wrong marker":   {'t', 'n', 'l', 2, 1, 1, 1, 'v', 0},
		"unknown format": {'t', 'n', 'l', 1, 2, 1, 1, 'v', 0},
		"no version":     {'t', 'n', 'l', 1, 1, 0},
		"cut short":      {'t', 'n', 'l', 1, 1, 1, 5, 'v'},
		"too long":       {'t', 'n', 'l', 1, 1, 1, 65},
	} {
		if v, ok := PingVersion(b); ok {
			t.Errorf("%s: read %q", name, v)
		}
	}
}
