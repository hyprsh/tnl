// Package token is the join token: what `tnl outpost add` prints and
// `tnl connect --token` reads. Its encoding is recorded in docs/spec/v1.md,
// Join token, and must never change; new fields go into the join exchange.
//
// It imports nothing heavy, so the Outpost build links it.
package token

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/hyprsh/tnl/internal/keys"
)

// Marker starts every token of this format.
const Marker = "tnl1."

// Token is everything an Outpost needs to bring the Tunnel up.
type Token struct {
	Address     string     // the Edge's --address, a DNS name or an IP
	Port        int        // the Edge's WireGuard port
	EdgeKey     keys.Key   // the Edge's WireGuard public key
	OneTimeKey  keys.Key   // the one-time private key
	TunnelAddr  netip.Addr // the Outpost's Tunnel address
	EdgeVersion string
}

// body is the JSON document inside the token. Keys are short and fixed.
type body struct {
	Address     string `json:"a"`
	Port        int    `json:"p"`
	EdgeKey     string `json:"k"`
	OneTimeKey  string `json:"o"`
	TunnelAddr  string `json:"t"`
	EdgeVersion string `json:"v"`
}

// Encode returns the token text: `tnl1.` and the unpadded base64url of a JSON
// object. It is one shell-safe word.
func (t Token) Encode() string {
	b, _ := json.Marshal(body{
		Address: t.Address, Port: t.Port,
		EdgeKey: text(t.EdgeKey), OneTimeKey: text(t.OneTimeKey),
		TunnelAddr: t.TunnelAddr.String(), EdgeVersion: t.EdgeVersion,
	})
	return Marker + base64.RawURLEncoding.EncodeToString(b)
}

func text(k keys.Key) string { b, _ := k.MarshalText(); return string(b) }

// UnknownMarkerError is a token this tnl can't read because it was made by a
// newer one (or isn't a token at all).
type UnknownMarkerError struct{ Token string }

func (e *UnknownMarkerError) Error() string {
	return fmt.Sprintf("this is not a join token this tnl can read (%s, expected %q): a newer tnl is needed; install the version the Edge runs", DescribeMarker(e.Token), strings.TrimSuffix(Marker, "."))
}

// DescribeMarker says which marker s starts with, for a message about text this
// tnl can't read. It names the marker only when it is a join token's (tnl2,
// tnl3, ...), and never any other part of s, which may be a key or a token.
func DescribeMarker(s string) string {
	head, _, _ := strings.Cut(strings.TrimSpace(s), ".")
	if rest, ok := strings.CutPrefix(head, "tnl"); ok {
		if n := len(rest); n > 0 && n <= 4 && strings.Trim(rest, "0123456789") == "" {
			return fmt.Sprintf("marker %q", head)
		}
	}
	return "no marker of tnl's"
}

// Decode parses a token. A marker other than tnl1 gives *UnknownMarkerError.
func Decode(s string) (Token, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "tnlc1.") {
		return Token{}, errors.New("this is an Outpost credential, not a join token: pass it as --credential-file (TNL_CREDENTIAL_FILE), or in TNL_CREDENTIAL")
	}
	payload, ok := strings.CutPrefix(s, Marker)
	if !ok {
		return Token{}, &UnknownMarkerError{Token: s}
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Token{}, errors.New("the join token is damaged (not base64); copy it again whole")
	}
	var b body
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil || dec.More() {
		return Token{}, errors.New("the join token is damaged; copy it again whole")
	}
	t := Token{Address: b.Address, Port: b.Port, EdgeVersion: b.EdgeVersion}
	if t.Address == "" || t.Port < 1 || t.Port > 65535 {
		return Token{}, errors.New("the join token is damaged: no usable Edge address")
	}
	if err := t.EdgeKey.UnmarshalText([]byte(b.EdgeKey)); err != nil {
		return Token{}, fmt.Errorf("the join token is damaged: Edge key: %v", err)
	}
	if err := t.OneTimeKey.UnmarshalText([]byte(b.OneTimeKey)); err != nil {
		return Token{}, fmt.Errorf("the join token is damaged: key: %v", err)
	}
	if t.TunnelAddr, err = netip.ParseAddr(b.TunnelAddr); err != nil {
		return Token{}, fmt.Errorf("the join token is damaged: Tunnel address: %v", err)
	}
	return t, nil
}

// Hash is the hex SHA-256 of the token text, which the Outpost's state keeps
// so a restart with the same token is recognised.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(sum[:])
}
