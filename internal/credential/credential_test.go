package credential

import (
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"sort"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/keys"
)

// golden was made with Python's json and base64 from the fixed keys below. The
// format is frozen: this string must keep decoding, and Encode must keep
// producing it.
const golden = "tnlc1.eyJuIjoibmFzIiwiYSI6ImVkZ2UuZXhhbXBsZS5jb20iLCJwIjo1MTgyMCwiayI6IkFBRUNBd1FGQmdjSUNRb0xEQTBPRHhBUkVoTVVGUllYR0JrYUd4d2RIaDg9IiwibyI6IklDRWlJeVFsSmljb0tTb3JMQzB1THpBeE1qTTBOVFkzT0RrNk96dzlQajg9IiwidCI6IjEwMC44NS4yMzMuODYifQ"

func fixed() Credential {
	var edge, priv keys.Key
	for i := range edge {
		edge[i] = byte(i)
		priv[i] = byte(32 + i)
	}
	return Credential{Name: "nas", Address: "edge.example.com", Port: 51820, EdgeKey: edge, PrivateKey: priv, TunnelAddr: netip.MustParseAddr("100.85.233.86")}
}

func TestEncodeMatchesTheGoldenString(t *testing.T) {
	if got := fixed().Encode(); got != golden {
		t.Fatalf("Encode = %s\nwant      %s", got, golden)
	}
}

func TestDecodeReadsTheGoldenString(t *testing.T) {
	got, err := Decode("  " + golden + "\n")
	if err != nil || got != fixed() {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestTheObjectHoldsExactlyTheKeysNAPKOT(t *testing.T) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(fixed().Encode(), Marker))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var names []string
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	if strings.Join(names, " ") != "a k n o p t" {
		t.Fatalf("keys %v", names)
	}
}

func TestAnyOtherKeyIsRefused(t *testing.T) {
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(golden, Marker))
	extra := strings.TrimSuffix(string(raw), "}") + `,"v":"1.0.0"}`
	if _, err := Decode(Marker + base64.RawURLEncoding.EncodeToString([]byte(extra))); err == nil {
		t.Fatal("a credential with a v key was accepted")
	}
}

func TestAJoinTokenIsAFriendlyError(t *testing.T) {
	_, err := Decode("tnl1.abc")
	if err == nil || !strings.Contains(err.Error(), "join token") || !strings.Contains(err.Error(), "--token") {
		t.Fatalf("got %v", err)
	}
}

func TestNoPartOfTheInputIsEchoed(t *testing.T) {
	for in, needle := range map[string]string{"tnlc2secretsecret.x": "secret", "tnl2.abcd": "tnl2"} {
		_, err := Decode(in)
		if err == nil || strings.Contains(err.Error(), needle) {
			t.Errorf("%q: the error echoes the input: %v", in, err)
		}
	}
	if _, err := Decode("tnlc2.abcd"); err == nil || !strings.Contains(err.Error(), `"tnlc2"`) {
		t.Errorf("a tnlc marker is named: %v", err)
	}
}

func TestDamagedCredentialsAreRefusedWithoutEchoingThem(t *testing.T) {
	for _, in := range []string{"", "tnlc1.", "tnlc1.!!!", "tnlc1.e30", "tnlc2.abc", "secret-looking-thing"} {
		_, err := Decode(in)
		if err == nil {
			t.Errorf("%q was accepted", in)
			continue
		}
		if in != "" && in != "tnlc1." && strings.Contains(err.Error(), in) {
			t.Errorf("%q: the error echoes the input: %v", in, err)
		}
	}
}
