package totp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"unicode"

	"inbok/internal/passwordhash"
)

const (
	backupSetSize    = 10 // Product policy: ten single-use codes per set.
	backupCodeLength = 10
	backupGroupSize  = 5                                 // Display as xxxxx-xxxxx for manual entry.
	backupAlphabet   = "23456789ABCDEFGHJKMNPQRSTUVWXYZ" // Omit 0/O and 1/I/L.
)

// BackupCodes separates display-only plaintext from the hashes sent to storage.
// Return Codes to the user only after the operation saving Hashes commits.
type BackupCodes struct {
	Codes  []string
	Hashes []string
}

var errBackupHash = errors.New("invalid stored backup-code hash")

// ErrNotChecked means a backup code check ended before it compared the code
// with any stored hash, so it made no guess. It comes with the context's
// error.
var ErrNotChecked = errors.New("backup code not checked")

// NewBackupCodes creates distinct codes with an independently salted hash
// each. It returns the context's error if ctx ends while waiting to hash.
func NewBackupCodes(ctx context.Context) (BackupCodes, error) {
	var set BackupCodes
	seen := make(map[string]struct{}, backupSetSize)
	for len(set.Codes) < backupSetSize {
		code, err := newBackupCode()
		if err != nil {
			return BackupCodes{}, err
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		hash, err := passwordhash.Hash(ctx, code)
		if err != nil {
			return BackupCodes{}, err
		}
		set.Hashes = append(set.Hashes, hash)
		set.Codes = append(set.Codes, code[:backupGroupSize]+"-"+code[backupGroupSize:])
	}
	return set, nil
}

func newBackupCode() (string, error) {
	code := make([]byte, backupCodeLength)
	alphabetSize := big.NewInt(int64(len(backupAlphabet)))
	for i := range code {
		// rand.Int samples uniformly; modulo would bias a 31-symbol alphabet.
		index, err := rand.Int(rand.Reader, alphabetSize)
		if err != nil {
			return "", err
		}
		code[i] = backupAlphabet[index.Int64()]
	}
	return string(code), nil
}

func normalizeBackupCode(input string) (string, error) {
	code := make([]byte, 0, backupCodeLength)
	for _, char := range input {
		if char == '-' || unicode.IsSpace(char) {
			continue
		}
		if char >= 'a' && char <= 'z' {
			char -= 'a' - 'A'
		}
		if len(code) == backupCodeLength || !strings.ContainsRune(backupAlphabet, char) {
			return "", ErrInvalidCode
		}
		code = append(code, byte(char))
	}
	if len(code) != backupCodeLength {
		return "", ErrInvalidCode
	}
	return string(code), nil
}

// CheckBackupCode ignores case, whitespace, and dashes before verifying a hash.
// A match does not consume the code; storage must delete its row atomically.
// It returns the context's error if ctx ends while waiting to verify.
func CheckBackupCode(ctx context.Context, input, stored string) error {
	code, err := normalizeBackupCode(input)
	if err != nil {
		return err
	}
	match, err := passwordhash.Verify(ctx, code, stored)
	if errors.Is(err, passwordhash.ErrInvalidHash) {
		return errBackupHash
	}
	if err != nil {
		return err
	}
	if !match {
		return ErrInvalidCode
	}
	return nil
}

// MatchBackupCode returns the index in stored of the hash input matches,
// normalizing input as [CheckBackupCode] does. It returns [ErrInvalidCode]
// when input is malformed or matches none, and the error of
// [CheckBackupCode] for a damaged hash or a context that ends while waiting
// to verify. A context that ends before the first comparison also matches
// [ErrNotChecked].
func MatchBackupCode(ctx context.Context, input string, stored []string) (int, error) {
	// Independently salted hashes require checking each remaining code.
	for i, hash := range stored {
		err := CheckBackupCode(ctx, input, hash)
		if err == nil {
			return i, nil
		}
		if errors.Is(err, ErrInvalidCode) {
			continue
		}
		// Only the first wait ends with nothing compared. After a
		// comparison the check counts, or a client that leaves mid-check
		// would get free guesses.
		if i == 0 && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return 0, fmt.Errorf("%w: %w", ErrNotChecked, err)
		}
		return 0, err
	}
	return 0, ErrInvalidCode
}
