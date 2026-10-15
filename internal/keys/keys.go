// Package keys generates and encodes WireGuard keys.
package keys

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
)

// Key is a WireGuard private or public key.
type Key [32]byte

// Hex is the encoding wireguard-go's configuration uses.
func (k Key) Hex() string { return hex.EncodeToString(k[:]) }

// Generate returns a new key pair.
func Generate() (private, public Key, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Key{}, Key{}, err
	}
	return Key(k.Bytes()), Key(k.PublicKey().Bytes()), nil
}
