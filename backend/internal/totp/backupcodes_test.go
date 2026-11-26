package totp

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/passwordhash"
)

func TestNewBackupCodes(t *testing.T) {
	set, err := NewBackupCodes(t.Context())
	require.NoError(t, err)
	require.Len(t, set.Codes, 10)
	require.Len(t, set.Hashes, 10)
	seen := make(map[string]struct{})
	for i, code := range set.Codes {
		require.Regexp(t, `^[2-9A-HJKMNP-Z]{5}-[2-9A-HJKMNP-Z]{5}$`, code)
		_, duplicate := seen[code]
		assert.False(t, duplicate)
		seen[code] = struct{}{}
		require.NoError(t, VerifyBackupCode(t.Context(), code, set.Hashes[i]))
		assert.Regexp(t, `^\$argon2id\$v=19\$m=19456,t=2,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`, set.Hashes[i])
		assert.NotContains(t, set.Hashes[i], strings.ReplaceAll(code, "-", ""))
	}
}

func TestParseBackupCode(t *testing.T) {
	for _, value := range []string{"ABCDE-FGHJK", "abcdefghjk", " abcde - fghjk ", "a b c d e\tf g h j k\n", "abcde\u00a0-fghjk"} {
		t.Run(value, func(t *testing.T) {
			normalized, err := parseBackupCode(value)
			require.NoError(t, err)
			assert.Equal(t, "ABCDEFGHJK", normalized)
		})
	}
}

func TestParseBackupInvalid(t *testing.T) {
	for _, value := range []string{"", "ABCDE", "ABCDEFGHJKM", "ABCDE-FGHIJ", "ABCDE-FGHLK", "ABCDE-FGHOJ", "ABCDE-FGHJ0", "ABCDE-FGHJ1", "ABCDE_FGHJK", "ABCDE-FGHJ\u212a"} {
		t.Run(value, func(t *testing.T) {
			normalized, err := parseBackupCode(value)
			require.ErrorIs(t, err, ErrInvalidCode)
			assert.Empty(t, normalized)
		})
	}
}

func TestVerifyBackupCode(t *testing.T) {
	first := hashPassword(t, "ABCDEFGHJK")
	second := hashPassword(t, "ABCDEFGHJK")
	firstParts := strings.Split(first, "$")
	secondParts := strings.Split(second, "$")
	require.Len(t, firstParts, 6)
	require.Len(t, secondParts, 6)
	assert.NotEqual(t, firstParts[4], secondParts[4])
	assert.NotEqual(t, firstParts[5], secondParts[5])
	require.NoError(t, VerifyBackupCode(t.Context(), " abcde - fghjk ", first))
	require.ErrorIs(t, VerifyBackupCode(t.Context(), "ABCDE-FGHJM", first), ErrInvalidCode)
	require.ErrorIs(t, VerifyBackupCode(t.Context(), "invalid-code", first), ErrInvalidCode)
}

func TestVerifyBackupHashInvalid(t *testing.T) {
	valid := hashPassword(t, "ABCDEFGHJK")
	for _, encoded := range []string{
		"",
		"private-hash",
		strings.Replace(valid, "argon2id", "argon2i", 1),
		strings.Replace(valid, "m=19456", "m=262145", 1),
		valid + "\n",
	} {
		t.Run(encoded, func(t *testing.T) {
			err := VerifyBackupCode(t.Context(), "ABCDE-FGHJK", encoded)
			require.ErrorIs(t, err, errBackupHash)
			assert.NotContains(t, err.Error(), "ABCDEFGHJK")
			assert.NotContains(t, err.Error(), "private-hash")
		})
	}
}

// hashPassword returns password's Argon2id hash, failing the test on an
// error.
func hashPassword(t *testing.T, password string) string {
	t.Helper()
	hash, err := passwordhash.Hash(t.Context(), password)
	require.NoError(t, err)
	return hash
}

// A context that ends before the hash reaches the caller as itself, not as a
// damaged stored hash.
func TestVerifyBackupCodeContext(t *testing.T) {
	stored := hashPassword(t, "ABCDEFGHJK")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := VerifyBackupCode(ctx, "ABCDE-FGHJK", stored)

	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, errBackupHash)
}

func TestMatchBackupCode(t *testing.T) {
	stored := []string{hashPassword(t, "ZZZZZZZZZZ"), hashPassword(t, "ABCDEFGHJK")}

	matched, matchErr := MatchBackupCode(t.Context(), "abcde-fghjk", stored)
	_, wrongErr := MatchBackupCode(t.Context(), "ABCDE-FGHJM", stored)
	_, noneErr := MatchBackupCode(t.Context(), "ABCDE-FGHJK", nil)
	_, damagedErr := MatchBackupCode(t.Context(), "ABCDE-FGHJK", []string{"damaged"})

	require.NoError(t, matchErr)
	assert.Equal(t, 1, matched)
	require.ErrorIs(t, wrongErr, ErrInvalidCode)
	require.ErrorIs(t, noneErr, ErrInvalidCode)
	require.ErrorIs(t, damagedErr, errBackupHash)
	assert.NotErrorIs(t, damagedErr, ErrNotVerified)
}

// A context that ends before the first comparison checked nothing.
func TestMatchBackupCodeEnded(t *testing.T) {
	stored := []string{hashPassword(t, "ABCDEFGHJK")}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := MatchBackupCode(ctx, "ABCDE-FGHJK", stored)

	require.ErrorIs(t, err, ErrNotVerified)
	require.ErrorIs(t, err, context.Canceled)
}
