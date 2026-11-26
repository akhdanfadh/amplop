package totp

import (
	"encoding/base32"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RFC 6238's SHA-1 secret, encoded as base32.
const vectorSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func TestVerifyVectors(t *testing.T) {
	// RFC 6238 Appendix B: the last six digits of each SHA-1 result.
	cases := []struct {
		at   int64
		code string
		step int64
	}{
		{59, "287082", 1},
		{1111111109, "081804", 37037036},
		{1111111111, "050471", 37037037},
		{1234567890, "005924", 41152263},
		{2000000000, "279037", 66666666},
		{20000000000, "353130", 666666666},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			clock := &fakeClock{now: time.Unix(c.at, 0)}
			verifier := NewVerifier(clock.Now)

			step, err := verifier.Verify(vectorSecret, c.code, -1)

			require.NoError(t, err)
			assert.Equal(t, c.step, step)
		})
	}
}

func TestVerifyWindow(t *testing.T) {
	// RFC 4226 counters 1 through 5. At Unix second 90 the current step is 3.
	cases := []struct {
		name, code string
		step       int64
		valid      bool
	}{
		{"previous", "359152", 2, true},
		{"current", "969429", 3, true},
		{"next", "338314", 4, true},
		{"too old", "287082", 0, false},
		{"too new", "254676", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clock := &fakeClock{now: time.Unix(90, 0)}
			step, err := NewVerifier(clock.Now).Verify(vectorSecret, c.code, -1)
			if !c.valid {
				require.ErrorIs(t, err, ErrInvalidCode)
				assert.Zero(t, step)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.step, step)
		})
	}
}

func TestVerifyReplay(t *testing.T) {
	clock := &fakeClock{now: time.Unix(90, 0)}
	verifier := NewVerifier(clock.Now)
	accepted, err := verifier.Verify(vectorSecret, "969429", -1)
	require.NoError(t, err)

	// A repeated or older step cannot pass after storage records acceptance.
	for _, code := range []string{"969429", "359152"} {
		_, err := verifier.Verify(vectorSecret, code, accepted)
		require.ErrorIs(t, err, ErrInvalidCode)
	}
	newer, err := verifier.Verify(vectorSecret, "338314", accepted)
	require.NoError(t, err)
	assert.Equal(t, int64(4), newer)

	// Accepting a clock-ahead code consumes it even when server time catches up.
	clock.Advance(30 * time.Second)
	_, err = verifier.Verify(vectorSecret, "338314", newer)
	require.ErrorIs(t, err, ErrInvalidCode)
}

func TestVerifyBoundary(t *testing.T) {
	clock := &fakeClock{now: time.Unix(90, 0).Add(-time.Nanosecond)}
	verifier := NewVerifier(clock.Now)
	_, err := verifier.Verify(vectorSecret, "338314", -1)
	require.ErrorIs(t, err, ErrInvalidCode)

	clock.Advance(time.Nanosecond)
	step, err := verifier.Verify(vectorSecret, "338314", -1)
	require.NoError(t, err)
	assert.Equal(t, int64(4), step)
}

func TestVerifyInvalidCode(t *testing.T) {
	for _, code := range []string{"", "12345", "1234567", " 969429", "969429 ", "96942x", "96942\n", "\u0669\u0666\u0669\u0664\u0662\u0669"} {
		t.Run(code, func(t *testing.T) {
			clock := &fakeClock{now: time.Unix(90, 0)}
			step, err := NewVerifier(clock.Now).Verify(vectorSecret, code, -1)
			require.ErrorIs(t, err, ErrInvalidCode)
			assert.Zero(t, step)
		})
	}
}

func TestVerifyInvalidSecret(t *testing.T) {
	for _, secret := range []string{"", "private-invalid-secret", "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"} {
		t.Run(secret, func(t *testing.T) {
			clock := &fakeClock{now: time.Unix(90, 0)}
			step, err := NewVerifier(clock.Now).Verify(secret, "969429", -1)
			require.ErrorIs(t, err, errSecret)
			assert.Zero(t, step)
			if secret != "" {
				assert.NotContains(t, err.Error(), secret)
			}
		})
	}
}

func TestVerifyEpoch(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	verifier := NewVerifier(clock.Now)
	step, err := verifier.Verify(vectorSecret, "755224", -1)
	require.NoError(t, err)
	assert.Zero(t, step)

	clock.Advance(-time.Nanosecond)
	_, err = verifier.Verify(vectorSecret, "755224", -1)
	require.ErrorIs(t, err, errClock)
}

func TestNewEnrollment(t *testing.T) {
	first, err := newEnrollment("alice+mail@example.com")
	require.NoError(t, err)
	second, err := newEnrollment("alice+mail@example.com")
	require.NoError(t, err)
	assert.NotEqual(t, first.secret, second.secret)

	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(first.secret)
	require.NoError(t, err)
	assert.Len(t, raw, 20)
	uri, err := url.Parse(first.uri)
	require.NoError(t, err)
	assert.Equal(t, "otpauth", uri.Scheme)
	assert.Equal(t, "totp", uri.Host)
	assert.Equal(t, "/amplop:alice+mail@example.com", uri.Path)
	query := uri.Query()
	assert.Equal(t, first.secret, query.Get("secret"))
	assert.Equal(t, "amplop", query.Get("issuer"))
	assert.Equal(t, "SHA1", query.Get("algorithm"))
	assert.Equal(t, "6", query.Get("digits"))
	assert.Equal(t, "30", query.Get("period"))
}

func TestNewEnrollmentNoEmail(t *testing.T) {
	value, err := newEnrollment("")
	require.Error(t, err)
	assert.Empty(t, value)
}
