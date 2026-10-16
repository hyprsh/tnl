// Package credential is the Outpost credential: what `tnl credential` prints
// and `tnl connect --credential-file` reads. Its encoding is recorded in
// docs/spec/v1.md, Outpost credential, and must never change.
//
// It imports nothing heavy, so the Outpost build links it.
package credential

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/keys"
)

// Marker starts every credential of this format.
const Marker = "tnlc1."

// JoinMarker starts a join token, which is not a credential.
const joinMarker = "tnl1."

// Credential is everything a declared Outpost needs to bring the Tunnel up and
// connect from the first packet.
type Credential struct {
	Name       string     // the Outpost's name
	Address    string     // the Edge's --address, a DNS name or an IP
	Port       int        // the Edge's WireGuard port
	EdgeKey    keys.Key   // the Edge's WireGuard public key
	PrivateKey keys.Key   // the Outpost's WireGuard private key
	TunnelAddr netip.Addr // the Outpost's Tunnel address, derived from its name
}

// body is the JSON document inside the credential. Keys are short and fixed.
type body struct {
	Name       string `json:"n"`
	Address    string `json:"a"`
	Port       int    `json:"p"`
	EdgeKey    string `json:"k"`
	PrivateKey string `json:"o"`
	TunnelAddr string `json:"t"`
}

// Encode returns the credential text: `tnlc1.` and the unpadded base64url of a
// JSON object. It is one shell-safe word.
func (c Credential) Encode() string {
	b, _ := json.Marshal(body{
		Name: c.Name, Address: c.Address, Port: c.Port,
		EdgeKey: text(c.EdgeKey), PrivateKey: text(c.PrivateKey), TunnelAddr: c.TunnelAddr.String(),
	})
	return Marker + base64.RawURLEncoding.EncodeToString(b)
}

func text(k keys.Key) string { b, _ := k.MarshalText(); return string(b) }

// Decode parses a credential. No error it returns carries the credential or a
// key.
func Decode(s string) (Credential, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, joinMarker) {
		return Credential{}, errors.New("this is a join token, not an Outpost credential: pass it as --token (TNL_TOKEN)")
	}
	payload, ok := strings.CutPrefix(s, Marker)
	if !ok {
		head, _, _ := strings.Cut(s, ".")
		if len(head) > 12 {
			head = head[:12]
		}
		return Credential{}, fmt.Errorf("this is not an Outpost credential this tnl can read (marker %q, expected %q): a newer tnl may be needed", head, strings.TrimSuffix(Marker, "."))
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Credential{}, errors.New("the Outpost credential is damaged (not base64); copy it again whole")
	}
	var b body
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil || dec.More() {
		return Credential{}, errors.New("the Outpost credential is damaged, or made by a newer tnl; copy it again whole")
	}
	c := Credential{Name: b.Name, Address: b.Address, Port: b.Port}
	if c.Name == "" {
		return Credential{}, errors.New("the Outpost credential is damaged: no name")
	}
	if c.Address == "" || c.Port < 1 || c.Port > 65535 {
		return Credential{}, errors.New("the Outpost credential is damaged: no usable Edge address")
	}
	if err := c.EdgeKey.UnmarshalText([]byte(b.EdgeKey)); err != nil {
		return Credential{}, errors.New("the Outpost credential is damaged: Edge key does not parse")
	}
	if err := c.PrivateKey.UnmarshalText([]byte(b.PrivateKey)); err != nil {
		return Credential{}, errors.New("the Outpost credential is damaged: key does not parse")
	}
	if c.TunnelAddr, err = netip.ParseAddr(b.TunnelAddr); err != nil {
		return Credential{}, errors.New("the Outpost credential is damaged: Tunnel address does not parse")
	}
	return c, nil
}

// Mint makes the credential for the named Outpost from the Edge's private key
// and where the Edge is reached, with a new key pair for the Outpost. It
// returns the Outpost's public key too, for the Edge's line. It never contacts
// an Edge.
func Mint(edgePrivate keys.Key, name, address string, port int) (Credential, keys.Key, error) {
	priv, pub, err := keys.Generate()
	if err != nil {
		return Credential{}, keys.Key{}, err
	}
	return Credential{
		Name: name, Address: address, Port: port,
		EdgeKey: edgePrivate.Public(), PrivateKey: priv, TunnelAddr: addr.Derive(name),
	}, pub, nil
}
