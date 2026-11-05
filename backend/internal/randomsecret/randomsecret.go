// Package randomsecret makes the random secrets inbok hands out, e.g. in a
// session cookie or an emailed link, and the hashes it saves instead of them.
// TOTP secrets are not these: inbok must read them back, so it encrypts them.
//
// DESIGN: Every secret has the same format. What one authorizes depends only on
// the table its hash is saved in.
package randomsecret

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// size is the number of random bytes: 256 bits cannot be guessed.
const size = 32

// errMalformed means text is not a secret [New] could have made.
var errMalformed = errors.New("malformed secret")

// New returns a random secret, encoded as unpadded URL-safe base64, and the
// SHA-256 hash to save in its place.
func New() (string, [sha256.Size]byte) {
	raw := make([]byte, size)
	rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw), sha256.Sum256(raw)
}

// Hash returns the SHA-256 hash of the bytes secret encodes, the key its
// saved row is found by. It returns an error when secret is not 32 bytes in
// unpadded URL-safe base64, as [New] makes them.
func Hash(secret string) ([sha256.Size]byte, error) {
	if len(secret) != base64.RawURLEncoding.EncodedLen(size) {
		return [sha256.Size]byte{}, errMalformed
	}

	// Even strict base64 ignores line breaks, so text of the right length can
	// decode short.
	raw, err := base64.RawURLEncoding.Strict().DecodeString(secret)
	if err != nil || len(raw) != size {
		return [sha256.Size]byte{}, errMalformed
	}
	return sha256.Sum256(raw), nil
}
