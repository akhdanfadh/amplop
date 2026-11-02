package randomsecret

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	first, firstHash := New()
	second, secondHash := New()

	raw, err := base64.RawURLEncoding.Strict().DecodeString(first)
	require.NoError(t, err)
	assert.Len(t, raw, 32)
	assert.Equal(t, sha256.Sum256(raw), firstHash)
	assert.NotEqual(t, first, second)
	assert.NotEqual(t, firstHash, secondHash)
}

// TestHash checks SHA-256 against a known vector of 32 zero bytes.
// The digest was checked independently with Python's hashlib.
func TestHash(t *testing.T) {
	hash, err := Hash(strings.Repeat("A", 43))

	require.NoError(t, err)
	assert.Equal(t, "66687aadf862bd776c8fc18b8e9f8e20089714856ee233b3902a591d0d5f2925", hex.EncodeToString(hash[:]))
}

func TestHashMalformed(t *testing.T) {
	cases := []struct {
		name, secret string
	}{
		{"empty", ""},
		{"short", strings.Repeat("A", 42)},
		{"long", strings.Repeat("A", 44)},
		{"oversized", strings.Repeat("A", 65536)},
		{"padding", strings.Repeat("A", 43) + "="},
		{"standard alphabet", strings.Repeat("/", 43)},
		{"noncanonical", strings.Repeat("A", 42) + "B"},
		{"newline", strings.Repeat("A", 43) + "\n"},
		{"newline within expected length", strings.Repeat("A", 42) + "\n"},
		{"embedded newline", strings.Repeat("A", 21) + "\r\n" + strings.Repeat("A", 22)},
		{"leading space", " " + strings.Repeat("A", 43)},
		{"trailing space", strings.Repeat("A", 43) + " "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hash, err := Hash(c.secret)

			require.ErrorIs(t, err, errMalformed)
			assert.Zero(t, hash)
		})
	}
}
