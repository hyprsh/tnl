// Package keys generates and encodes WireGuard keys.
package keys

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
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

// MarshalText encodes the key as base64, the form `wg` and state.json use.
func (k Key) MarshalText() ([]byte, error) {
	return []byte(base64.StdEncoding.EncodeToString(k[:])), nil
}

// UnmarshalText parses the base64 form.
func (k *Key) UnmarshalText(b []byte) error {
	raw, err := base64.StdEncoding.DecodeString(string(b))
	if err != nil || len(raw) != len(k) {
		return errors.New("not a base64 WireGuard key of 32 bytes")
	}
	copy(k[:], raw)
	return nil
}

// Public returns the public key for a private key.
func (k Key) Public() Key {
	p, err := ecdh.X25519().NewPrivateKey(k[:])
	if err != nil {
		return Key{}
	}
	return Key(p.PublicKey().Bytes())
}

// Fingerprint names a public key without revealing anything secret: "SHA256:"
// and the unpadded base64 of the SHA-256 of the 32 key bytes, as ssh-keygen -l
// prints. Call it on the public key, never the private one.
func (k Key) Fingerprint() string {
	sum := sha256.Sum256(k[:])
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}
