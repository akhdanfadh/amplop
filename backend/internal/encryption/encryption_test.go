package encryption_test

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/encryption"
)

func key(seed byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 32))
}

func TestSealOpen(t *testing.T) {
	ring, err := encryption.New("1:" + key(1))
	require.NoError(t, err)
	plaintext := []byte("authenticator secret")
	id, sealed, err := ring.Seal(plaintext, "totp:42")
	require.NoError(t, err)
	assert.Equal(t, "1", id)
	assert.NotContains(t, string(sealed), string(plaintext))
	opened, err := ring.Open(id, sealed, "totp:42")
	require.NoError(t, err)
	assert.Equal(t, plaintext, opened)
	_, second, err := ring.Seal(plaintext, "totp:42")
	require.NoError(t, err)
	assert.NotEqual(t, sealed, second, "each encryption needs a fresh nonce")
}

func TestOpenRejectsChanges(t *testing.T) {
	ring, err := encryption.New("1:" + key(1) + ",2:" + key(2))
	require.NoError(t, err)
	id, sealed, err := ring.Seal([]byte("secret"), "totp:42")
	require.NoError(t, err)
	damaged := bytes.Clone(sealed)
	damaged[len(damaged)-1] ^= 1
	for _, c := range []struct {
		name, id, owner string
		data            []byte
	}{
		{"owner", id, "totp:43", sealed},
		{"key", "2", "totp:42", sealed},
		{"unknown key", "3", "totp:42", sealed},
		{"tampered", id, "totp:42", damaged},
		{"empty", id, "totp:42", nil},
		{"truncated", id, "totp:42", sealed[:10]},
	} {
		t.Run(c.name, func(t *testing.T) {
			opened, err := ring.Open(c.id, c.data, c.owner)
			require.Error(t, err)
			assert.Empty(t, opened)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestKeyRotation(t *testing.T) {
	old, err := encryption.New("1:" + key(1))
	require.NoError(t, err)
	id, sealed, err := old.Seal([]byte("secret"), "totp:42")
	require.NoError(t, err)
	rotated, err := encryption.New("2:" + key(2) + ",1:" + key(1))
	require.NoError(t, err)
	opened, err := rotated.Open(id, sealed, "totp:42")
	require.NoError(t, err)
	assert.Equal(t, []byte("secret"), opened)
	newID, _, err := rotated.Seal(opened, "totp:42")
	require.NoError(t, err)
	assert.Equal(t, "2", newID)
}

func TestInvalidKeys(t *testing.T) {
	for _, value := range []string{"", " ", "1", "1:", "0:" + key(1), "-1:" + key(1), "01:" + key(1), "x:" + key(1), "4294967296:" + key(1), "1:invalid-key", "1:" + base64.StdEncoding.EncodeToString(make([]byte, 16)), "1:" + key(1) + ",1:" + key(2), "1:" + key(1) + ","} {
		t.Run(value, func(t *testing.T) {
			ring, err := encryption.New(value)
			require.Error(t, err)
			assert.Nil(t, ring)
			assert.NotContains(t, err.Error(), key(1))
			assert.NotContains(t, err.Error(), "invalid-key")
		})
	}
}
