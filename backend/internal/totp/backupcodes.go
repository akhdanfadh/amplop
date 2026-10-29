package totp

import (
	"crypto/rand"
	"errors"
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

// NewBackupCodes creates distinct codes with an independently salted hash each.
func NewBackupCodes() (BackupCodes, error) {
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
		set.Hashes = append(set.Hashes, passwordhash.Hash(code))
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
func CheckBackupCode(input, stored string) error {
	code, err := normalizeBackupCode(input)
	if err != nil {
		return err
	}
	match, err := passwordhash.Verify(code, stored)
	if err != nil {
		return errBackupHash
	}
	if !match {
		return ErrInvalidCode
	}
	return nil
}
