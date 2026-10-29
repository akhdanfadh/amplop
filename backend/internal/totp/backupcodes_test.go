package totp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/passwordhash"
)

func TestNewBackupCodes(t *testing.T) {
	set, err := NewBackupCodes()
	require.NoError(t, err)
	require.Len(t, set.Codes, 10)
	require.Len(t, set.Hashes, 10)
	seen := make(map[string]struct{})
	for i, code := range set.Codes {
		require.Regexp(t, `^[2-9A-HJKMNP-Z]{5}-[2-9A-HJKMNP-Z]{5}$`, code)
		_, duplicate := seen[code]
		assert.False(t, duplicate)
		seen[code] = struct{}{}
		require.NoError(t, CheckBackupCode(code, set.Hashes[i]))
		assert.Regexp(t, `^\$argon2id\$v=19\$m=19456,t=2,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`, set.Hashes[i])
		assert.NotContains(t, set.Hashes[i], strings.ReplaceAll(code, "-", ""))
	}
}

func TestNormalizeBackupCode(t *testing.T) {
	for _, value := range []string{"ABCDE-FGHJK", "abcdefghjk", " abcde - fghjk ", "a b c d e\tf g h j k\n", "abcde\u00a0-fghjk"} {
		t.Run(value, func(t *testing.T) {
			normalized, err := normalizeBackupCode(value)
			require.NoError(t, err)
			assert.Equal(t, "ABCDEFGHJK", normalized)
		})
	}
}

func TestNormalizeBackupInvalid(t *testing.T) {
	for _, value := range []string{"", "ABCDE", "ABCDEFGHJKM", "ABCDE-FGHIJ", "ABCDE-FGHLK", "ABCDE-FGHOJ", "ABCDE-FGHJ0", "ABCDE-FGHJ1", "ABCDE_FGHJK", "ABCDE-FGHJ\u212a"} {
		t.Run(value, func(t *testing.T) {
			normalized, err := normalizeBackupCode(value)
			require.ErrorIs(t, err, ErrInvalidCode)
			assert.Empty(t, normalized)
		})
	}
}

func TestCheckBackupCode(t *testing.T) {
	first := passwordhash.Hash("ABCDEFGHJK")
	second := passwordhash.Hash("ABCDEFGHJK")
	firstParts := strings.Split(first, "$")
	secondParts := strings.Split(second, "$")
	require.Len(t, firstParts, 6)
	require.Len(t, secondParts, 6)
	assert.NotEqual(t, firstParts[4], secondParts[4])
	assert.NotEqual(t, firstParts[5], secondParts[5])
	require.NoError(t, CheckBackupCode(" abcde - fghjk ", first))
	require.ErrorIs(t, CheckBackupCode("ABCDE-FGHJM", first), ErrInvalidCode)
	require.ErrorIs(t, CheckBackupCode("invalid-code", first), ErrInvalidCode)
}

func TestCheckBackupHashInvalid(t *testing.T) {
	valid := passwordhash.Hash("ABCDEFGHJK")
	for _, encoded := range []string{
		"",
		"private-hash",
		strings.Replace(valid, "argon2id", "argon2i", 1),
		strings.Replace(valid, "m=19456", "m=262145", 1),
		valid + "\n",
	} {
		t.Run(encoded, func(t *testing.T) {
			err := CheckBackupCode("ABCDE-FGHJK", encoded)
			require.ErrorIs(t, err, errBackupHash)
			assert.NotContains(t, err.Error(), "ABCDEFGHJK")
			assert.NotContains(t, err.Error(), "private-hash")
		})
	}
}
