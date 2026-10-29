package passwordhash

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHash(t *testing.T) {
	const password = "a long password with spaces"

	encoded := Hash(password)
	match, err := Verify(password, encoded)

	require.NoError(t, err)
	assert.True(t, match)
	assert.Regexp(t, `^\$argon2id\$v=19\$m=19456,t=2,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`, encoded)
}

func TestHashSalt(t *testing.T) {
	const password = "a long password with spaces"

	first := Hash(password)
	second := Hash(password)

	firstParts := strings.Split(first, "$")
	secondParts := strings.Split(second, "$")
	require.Len(t, firstParts, 6)
	require.Len(t, secondParts, 6)
	assert.NotEqual(t, firstParts[4], secondParts[4])
	match, err := Verify(password, second)
	require.NoError(t, err)
	assert.True(t, match)
}

func TestVerify(t *testing.T) {
	const password = " a long password \u00e9\U0001f512\x00 "
	encoded := Hash(password)
	cases := []struct {
		name     string
		password string
		match    bool
	}{
		{"same", password, true},
		{"wrong", "a different password", false},
		{"empty", "", false},
		{"trimmed", strings.TrimSpace(password), false},
		{"changed suffix", password + "x", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			match, err := Verify(c.password, encoded)

			require.NoError(t, err)
			assert.Equal(t, c.match, match)
		})
	}
}

// These reference-CLI vectors come from x/crypto/argon2's TestVectors.
// They also check that verification uses stored costs and lengths.
func TestVerifyVectors(t *testing.T) {
	encoded := []string{
		"$argon2id$v=19$m=64,t=1,p=1$c29tZXNhbHQ$ZVrRXqxlLcWfcXCnMyv0m4Rpvh/bnCi7",
		"$argon2id$v=19$m=256,t=3,p=2$c29tZXNhbHQ$RmjTCsQYfmh47t6s8P2DxaCjDbLMFu8L",
	}

	for _, hash := range encoded {
		match, err := Verify("password", hash)

		require.NoError(t, err)
		assert.True(t, match)
	}
}

func TestVerifyMalformed(t *testing.T) {
	const encoded = "$argon2id$v=19$m=64,t=1,p=1$c29tZXNhbHQ$ZVrRXqxlLcWfcXCnMyv0m4Rpvh/bnCi7"
	cases := []struct {
		name string
		hash string
	}{
		{"empty", ""},
		{"not PHC", "not a password hash"},
		{"missing prefix", strings.TrimPrefix(encoded, "$")},
		{"extra field", encoded + "$extra"},
		{"other algorithm", strings.Replace(encoded, "argon2id", "argon2i", 1)},
		{"old version", strings.Replace(encoded, "v=19", "v=16", 1)},
		{"future version", strings.Replace(encoded, "v=19", "v=20", 1)},
		{"missing parameter", strings.Replace(encoded, ",p=1", "", 1)},
		{"parameter suffix", strings.Replace(encoded, "p=1", "p=1junk", 1)},
		{"negative memory", strings.Replace(encoded, "m=64", "m=-1", 1)},
		{"memory overflow", strings.Replace(encoded, "m=64", "m=4294967296", 1)},
		{"zero memory", strings.Replace(encoded, "m=64", "m=0", 1)},
		{"memory below lane limit", strings.Replace(encoded, "m=64", "m=7", 1)},
		{"memory below two lanes", strings.ReplaceAll(strings.Replace(encoded, "m=64", "m=8", 1), "p=1", "p=2")},
		{"memory over cap", strings.Replace(encoded, "m=64", "m=262145", 1)},
		{"zero iterations", strings.Replace(encoded, "t=1", "t=0", 1)},
		{"iterations over cap", strings.Replace(encoded, "t=1", "t=11", 1)},
		{"zero parallelism", strings.Replace(encoded, "p=1", "p=0", 1)},
		{"parallelism overflow", strings.Replace(encoded, "p=1", "p=256", 1)},
		{"parallelism over cap", strings.ReplaceAll(strings.Replace(encoded, "m=64", "m=256", 1), "p=1", "p=17")},
		{"empty salt", strings.Replace(encoded, "c29tZXNhbHQ", "", 1)},
		{"short salt", strings.Replace(encoded, "c29tZXNhbHQ", "MTIzNDU2Nw", 1)},
		{"invalid salt", strings.Replace(encoded, "c29tZXNhbHQ", "*", 1)},
		{"padded salt", strings.Replace(encoded, "c29tZXNhbHQ", "c29tZXNhbHQ=", 1)},
		{"noncanonical salt", strings.Replace(encoded, "c29tZXNhbHQ", "c29tZXNhbHR", 1)},
		{"newline in salt", strings.Replace(encoded, "c29tZXNhbHQ", "c29t\nZXNhbHQ", 1)},
		{"empty key", encoded[:strings.LastIndex(encoded, "$")+1]},
		{"short key", encoded[:strings.LastIndex(encoded, "$")+1] + "YWJj"},
		{"invalid key", encoded + "*"},
		{"newline in key", encoded + "\n"},
		{"oversized", strings.Replace(encoded, "c29tZXNhbHQ", strings.Repeat("YWFh", 64), 1)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			match, err := Verify("password", c.hash)

			require.ErrorIs(t, err, ErrInvalidHash)
			require.EqualError(t, err, "invalid password hash")
			assert.False(t, match)
		})
	}
}
