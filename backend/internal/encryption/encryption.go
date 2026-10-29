// Package encryption protects stored secrets with a rotating AES-256-GCM key list.
package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

const (
	keySize        = 32
	backupHashInfo = "inbok:totp-backup-codes:v1"
)

var (
	errKeys = errors.New("keys must be a comma-separated list of unique positive numeric IDs and base64-encoded 32-byte keys")
	errOpen = errors.New("cannot decrypt stored value")
	errKey  = errors.New("unknown hash key ID")
)

type key struct {
	cipher cipher.AEAD
	hash   []byte
}

// Keyring keeps the first key active and older keys available for reads.
// It is immutable after construction and safe for concurrent use.
type Keyring struct {
	current string
	keys    map[string]key
}

// New parses entries such as "2:<base64 key>,1:<base64 key>".
// IDs are canonical positive 32-bit decimal numbers. Errors omit key material.
func New(value string) (*Keyring, error) {
	ring := &Keyring{keys: make(map[string]key)}
	for entry := range strings.SplitSeq(value, ",") {
		id, encoded, ok := strings.Cut(strings.TrimSpace(entry), ":")
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

		// A dedicated derivation keeps backup-code hashes separate from encryption.
		hashKey, err := hkdf.Key(sha256.New, raw, nil, backupHashInfo, keySize)
		if err != nil {
			return nil, errKeys
		}
		ring.keys[id] = key{cipher: aead, hash: hashKey}
		if ring.current == "" {
			ring.current = id
		}
	}
	return ring, nil
}

// CurrentID identifies the key for new backup-code hashes.
func (r *Keyring) CurrentID() string {
	return r.current
}

// Seal returns a key ID and ciphertext containing its random nonce and tag.
// Store both together. Owner binds the value to its purpose and user, e.g. totp:42.
// Rotate keys before encrypting 2^32 values under any one key.
func (r *Keyring) Seal(plaintext []byte, owner string) (string, []byte, error) {
	k := r.keys[r.current]
	return r.current, k.cipher.Seal(nil, nil, plaintext, []byte(owner)), nil
}

// Open authenticates the owner and ciphertext before returning plaintext.
func (r *Keyring) Open(id string, ciphertext []byte, owner string) ([]byte, error) {
	k, ok := r.keys[id]
	if !ok {
		return nil, errOpen
	}
	plaintext, err := k.cipher.Open(nil, nil, ciphertext, []byte(owner))
	if err != nil {
		return nil, errOpen
	}
	return plaintext, nil
}

// Hash returns an HMAC-SHA256 of an already normalized backup code.
// Keep its key ID with the hash so rotation preserves existing codes.
func (r *Keyring) Hash(id string, value []byte) ([]byte, error) {
	k, ok := r.keys[id]
	if !ok {
		return nil, errKey
	}
	mac := hmac.New(sha256.New, k.hash)
	mac.Write(value)
	return mac.Sum(nil), nil
}
