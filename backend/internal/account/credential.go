// Package account owns local users and their credentials.
package account

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	// OWASP's minimum Argon2id profile: 19 MiB, two passes, one lane.
	passwordMemory      = 19 * 1024 // KiB
	passwordIterations  = 2
	passwordParallelism = 1
	passwordSaltLength  = 16
	passwordKeyLength   = 32

	passwordParams   = "m=%d,t=%d,p=%d"
	minMemoryPerLane = 8 // Argon2 requires at least eight KiB per lane.

	// Bound verification work even when the stored parameters are damaged.
	maxPasswordMemory      = 256 * 1024 // KiB
	maxPasswordIterations  = 10
	maxPasswordParallelism = 16
	maxPasswordHashLength  = 256
	minPasswordSaltLength  = 8
	minPasswordKeyLength   = 4
)

var errInvalidPasswordHash = errors.New("invalid password hash")

type passwordHash struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	salt        []byte
	key         []byte
}

// hashPassword stores the work parameters and a fresh salt with the hash.
// Password validation belongs to the caller; hashing preserves the input.
func hashPassword(password string) string {
	salt := make([]byte, passwordSaltLength)
	rand.Read(salt)
	key := argon2.IDKey([]byte(password), salt, passwordIterations, passwordMemory, passwordParallelism, passwordKeyLength)

	params := fmt.Sprintf(passwordParams, passwordMemory, passwordIterations, passwordParallelism)
	return fmt.Sprintf("$argon2id$v=%d$%s$%s$%s", argon2.Version, params,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// verifyPassword uses the stored parameters so changing defaults keeps old
// passwords usable. A mismatch is distinct from an unreadable stored hash.
func verifyPassword(password, encoded string) (bool, error) {
	hash, err := decodePasswordHash(encoded)
	if err != nil {
		return false, err
	}

	key := argon2.IDKey([]byte(password), hash.salt, hash.iterations, hash.memory, hash.parallelism, uint32(len(hash.key)))
	return subtle.ConstantTimeCompare(key, hash.key) == 1, nil
}

func decodePasswordHash(encoded string) (passwordHash, error) {
	if len(encoded) > maxPasswordHashLength || strings.ContainsAny(encoded, "\r\n") {
		return passwordHash{}, errInvalidPasswordHash
	}

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return passwordHash{}, errInvalidPasswordHash
	}

	var hash passwordHash
	_, err := fmt.Sscanf(parts[3], passwordParams, &hash.memory, &hash.iterations, &hash.parallelism)
	if err != nil || parts[3] != fmt.Sprintf(passwordParams, hash.memory, hash.iterations, hash.parallelism) {
		return passwordHash{}, errInvalidPasswordHash
	}
	if hash.iterations == 0 || hash.parallelism == 0 || hash.memory < minMemoryPerLane*uint32(hash.parallelism) {
		return passwordHash{}, errInvalidPasswordHash
	}
	if hash.memory > maxPasswordMemory || hash.iterations > maxPasswordIterations || hash.parallelism > maxPasswordParallelism {
		return passwordHash{}, errInvalidPasswordHash
	}

	encoding := base64.RawStdEncoding.Strict()
	hash.salt, err = encoding.DecodeString(parts[4])
	if err != nil || len(hash.salt) < minPasswordSaltLength {
		return passwordHash{}, errInvalidPasswordHash
	}
	hash.key, err = encoding.DecodeString(parts[5])
	if err != nil || len(hash.key) < minPasswordKeyLength {
		return passwordHash{}, errInvalidPasswordHash
	}

	return hash, nil
}
