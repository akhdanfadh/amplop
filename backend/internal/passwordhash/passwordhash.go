// Package passwordhash hashes secrets as Argon2id PHC strings.
package passwordhash

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/sync/semaphore"
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

	// memoryBudget caps the memory of the hashes in progress: six hashes of
	// passwordMemory at once.
	memoryBudget = 128 * 1024 // KiB

	// Bound verification work even when the stored parameters are damaged.
	// Any accepted hash fits in memoryBudget.
	maxPasswordMemory      = memoryBudget
	maxPasswordIterations  = 10
	maxPasswordParallelism = 16
	maxPasswordHashLength  = 256
	minPasswordSaltLength  = 8
	minPasswordKeyLength   = 4
)

// budget holds the memory, in KiB, of the Argon2id hashes in progress in
// the process. Each hash takes its own memory cost from it.
//
// DESIGN: Without a bound, requests from many clients at once each start a
// hash, since every throttle counts per client or per account, and enough
// of them exhaust memory. Hashes wait in arrival order; each waits until
// its memory is free or its context ends. The budget bounds memory, not
// cores: hashes beyond the cores share them and finish later, while the
// total work per second stays the same.
var budget = semaphore.NewWeighted(memoryBudget)

// ErrInvalidHash means the saved PHC string is malformed or exceeds work limits.
var ErrInvalidHash = errors.New("invalid password hash")

type passwordHash struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	salt        []byte
	key         []byte
}

// Hash returns password's Argon2id PHC string, which stores the work
// parameters and a fresh salt with the hash. It waits for its memory in the
// process's budget and returns the context's error if ctx ends first.
// Callers own input validation and normalization; hashing preserves the
// input.
func Hash(ctx context.Context, password string) (string, error) {
	salt := make([]byte, passwordSaltLength)
	rand.Read(salt)
	key, err := deriveKey(ctx, password, passwordHash{
		memory: passwordMemory, iterations: passwordIterations, parallelism: passwordParallelism, salt: salt,
	}, passwordKeyLength)
	if err != nil {
		return "", err
	}

	params := fmt.Sprintf(passwordParams, passwordMemory, passwordIterations, passwordParallelism)
	return fmt.Sprintf("$argon2id$v=%d$%s$%s$%s", argon2.Version, params,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify reports whether password matches the PHC string encoded, hashing
// with encoded's work parameters. It returns [ErrInvalidHash] for an
// unreadable encoded, without hashing. Like [Hash] it waits for its memory
// and returns the context's error if ctx ends first.
func Verify(ctx context.Context, password, encoded string) (bool, error) {
	hash, err := decodePasswordHash(encoded)
	if err != nil {
		return false, err
	}

	// The stored parameters, not the current ones, so changing the profile
	// keeps old passwords usable.
	key, err := deriveKey(ctx, password, hash, uint32(len(hash.key)))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(key, hash.key) == 1, nil
}

// deriveKey returns the Argon2id key of length for password with params'
// salt and costs once params' memory is free in the budget, or the
// context's error if ctx ends first.
func deriveKey(ctx context.Context, password string, params passwordHash, length uint32) ([]byte, error) {
	if err := budget.Acquire(ctx, int64(params.memory)); err != nil {
		return nil, err
	}
	defer budget.Release(int64(params.memory))

	return argon2.IDKey([]byte(password), params.salt, params.iterations, params.memory, params.parallelism, length), nil
}

func decodePasswordHash(encoded string) (passwordHash, error) {
	if len(encoded) > maxPasswordHashLength || strings.ContainsAny(encoded, "\r\n") {
		return passwordHash{}, ErrInvalidHash
	}

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return passwordHash{}, ErrInvalidHash
	}

	var hash passwordHash
	_, err := fmt.Sscanf(parts[3], passwordParams, &hash.memory, &hash.iterations, &hash.parallelism)
	if err != nil || parts[3] != fmt.Sprintf(passwordParams, hash.memory, hash.iterations, hash.parallelism) {
		return passwordHash{}, ErrInvalidHash
	}
	if hash.iterations == 0 || hash.parallelism == 0 || hash.memory < minMemoryPerLane*uint32(hash.parallelism) {
		return passwordHash{}, ErrInvalidHash
	}
	if hash.memory > maxPasswordMemory || hash.iterations > maxPasswordIterations || hash.parallelism > maxPasswordParallelism {
		return passwordHash{}, ErrInvalidHash
	}

	encoding := base64.RawStdEncoding.Strict()
	hash.salt, err = encoding.DecodeString(parts[4])
	if err != nil || len(hash.salt) < minPasswordSaltLength {
		return passwordHash{}, ErrInvalidHash
	}
	hash.key, err = encoding.DecodeString(parts[5])
	if err != nil || len(hash.key) < minPasswordKeyLength {
		return passwordHash{}, ErrInvalidHash
	}

	return hash, nil
}
