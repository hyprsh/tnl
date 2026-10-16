package token

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/keys"
)

func sample(t *testing.T) Token {
	t.Helper()
	_, edgePub, _ := keys.Generate()
	oneTime, _, _ := keys.Generate()
	return Token{
		Address:     "tnl.example.com",
		Port:        51820,
		EdgeKey:     edgePub,
		OneTimeKey:  oneTime,
		TunnelAddr:  netip.MustParseAddr("100.64.0.2"),
		EdgeVersion: "0.1.0",
	}
}

func TestRoundTrip(t *testing.T) {
	want := sample(t)
	s := want.Encode()
	if !strings.HasPrefix(s, "tnl1.") {
		t.Fatalf("token %q lacks the tnl1. marker", s)
	}
	if strings.ContainsAny(s, " \n+/=") {
		t.Fatalf("token %q is not one shell-safe word", s)
	}
	got, err := Decode(s)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestDecodeTrimsSurroundingWhitespace(t *testing.T) {
	want := sample(t)
	got, err := Decode("  " + want.Encode() + "\n")
	if err != nil || got != want {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestACredentialAsAJoinTokenIsAFriendlyError(t *testing.T) {
	_, err := Decode("tnlc1.abc")
	if err == nil || !strings.Contains(err.Error(), "Outpost credential") || !strings.Contains(err.Error(), "TNL_CREDENTIAL") {
		t.Fatalf("got %v", err)
	}
}

func TestUnknownMarkerSaysANewerTnlIsNeeded(t *testing.T) {
	for _, in := range []string{"tnl2.abcd", "tnl9.", "wg1.abcd", "nonsense"} {
		_, err := Decode(in)
		var um *UnknownMarkerError
		if !errors.As(err, &um) {
			t.Fatalf("%q: err = %v, want *UnknownMarkerError", in, err)
		}
		if !strings.Contains(err.Error(), "newer tnl") {
			t.Errorf("%q: %v does not say a newer tnl is needed", in, err)
		}
	}
}

func TestAnUnknownMarkerErrorEchoesNothingButTnlMarkers(t *testing.T) {
	_, err := Decode("tnl2.abcd")
	if err == nil || !strings.Contains(err.Error(), `"tnl2"`) {
		t.Fatalf("a tnl marker is named: %v", err)
	}
	for in, needle := range map[string]string{"hunter2hunter2.rest": "hunter", "wg1.abcd": "wg1", "nonsense-secret": "nonsense", "tnl2secretsecret.x": "secret", "tnlc2.abcd": "tnlc2"} {
		_, err := Decode(in)
		if err == nil || strings.Contains(err.Error(), needle) {
			t.Errorf("%q: the error echoes the input: %v", in, err)
		}
	}
}

func TestMalformedTokensAreRefused(t *testing.T) {
	good := sample(t).Encode()
	for name, in := range map[string]string{
		"empty payload": "tnl1.",
		"not base64":    "tnl1.!!!",
		"not json":      "tnl1.bm90IGpzb24",
		"truncated":     good[:len(good)-8],
	} {
		_, err := Decode(in)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		var um *UnknownMarkerError
		if errors.As(err, &um) {
			t.Errorf("%s: reported as an unknown marker", name)
		}
	}
}

func TestHashIsSHA256OfTheTokenText(t *testing.T) {
	const want = "426ab79e802abe39e3e5507bd9840aa94e242272fe011bc7b9dcfdcfa6c52259" // sha256 of "tnl1.abc"
	if got := Hash("tnl1.abc"); got != want {
		t.Fatalf("hash %q, want %q", got, want)
	}
	if Hash(" tnl1.abc\n") != want {
		t.Error("surrounding whitespace changes the hash")
	}
}
