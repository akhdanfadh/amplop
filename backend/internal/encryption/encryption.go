// Package encryption protects stored secrets with a rotating AES-256-GCM key list.
package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

const keySize = 32

var (
	errKeys = errors.New("keys must be a comma-separated list of unique positive numeric IDs and base64-encoded 32-byte keys")
	errOpen = errors.New("cannot decrypt stored value")
)

// Keyring keeps the first key active and older keys available for reads.
// It is immutable after construction and safe for concurrent use.
type Keyring struct {
	current string
	keys    map[string]cipher.AEAD
}

// New parses entries such as "2:<base64 key>,1:<base64 key>".
// IDs are canonical positive 32-bit decimal numbers. Errors omit key material.
func New(value string) (*Keyring, error) {
	ring := &Keyring{keys: make(map[string]cipher.AEAD)}
	for entry := range strings.SplitSeq(value, ",") {
		id, encoded, ok := strings.Cut(strings.TrimSpace(entry), ":")
		// amplop's own ID format: one spelling per number, since a stored
		// secret finds its key by the exact ID text saved with it.
		number, err := strconv.ParseUint(id, 10, 32)
		if !ok || err != nil || number == 0 || strconv.FormatUint(number, 10) != id {
			return nil, errKeys
		}
		if _, exists := ring.keys[id]; exists {
			return nil, errKeys
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(raw) != keySize {
			return nil, errKeys
		}
		block, err := aes.NewCipher(raw)
		if err != nil {
			return nil, errKeys
		}
		aead, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			return nil, errKeys
		}
		ring.keys[id] = aead
		if ring.current == "" {
			ring.current = id
		}
	}
	return ring, nil
}

// Seal returns a key ID and ciphertext containing its random nonce and tag.
// Store both together. Owner binds the value to its purpose and user, e.g. totp:42.
// Rotate keys before encrypting 2^32 values under any one key.
func (r *Keyring) Seal(plaintext []byte, owner string) (string, []byte, error) {
	return r.current, r.keys[r.current].Seal(nil, nil, plaintext, []byte(owner)), nil
}

// Open authenticates the owner and ciphertext before returning plaintext.
func (r *Keyring) Open(id string, ciphertext []byte, owner string) ([]byte, error) {
	aead, ok := r.keys[id]
	if !ok {
		return nil, errOpen
	}
	plaintext, err := aead.Open(nil, nil, ciphertext, []byte(owner))
	if err != nil {
		return nil, errOpen
	}
	return plaintext, nil
}
