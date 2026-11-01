package emailverification

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requested returns storage holding one link requested at testNow, the
// service, and the link's secret.
func requested(t *testing.T) (*memoryStorage, *Service, string) {
	t.Helper()

	storage, mailer := unverified(), &fakeMailer{}
	service := testService(storage, mailer)
	_, err := service.Request(t.Context(), testUserID)
	require.NoError(t, err)
	require.Len(t, mailer.sent, 1)
	return storage, service, secretOf(t, mailer.sent[0].Body)
}

// at returns service with its clock at now.
func at(service *Service, now time.Time) *Service {
	service.now = func() time.Time { return now }
	return service
}

// The emailed secret verifies the address at the time of redeeming.
func TestRedeem(t *testing.T) {
	storage, service, secret := requested(t)
	later := testNow.Add(time.Hour)

	err := at(service, later).Redeem(t.Context(), secret)
	require.NoError(t, err)

	assert.True(t, storage.state.Verified)
	assert.Equal(t, &later, storage.verifiedAt)
}

// A link for a user who is already verified succeeds and keeps their
// verified time.
func TestRedeemAlreadyVerified(t *testing.T) {
	storage, service, secret := requested(t)
	verifiedAt := testNow.Add(-time.Hour)
	storage.state.Verified, storage.verifiedAt = true, &verifiedAt

	err := service.Redeem(t.Context(), secret)
	require.NoError(t, err)

	assert.Equal(t, &verifiedAt, storage.verifiedAt)
}

// An expired link, or one sent to an address the user no longer has, is not
// valid and changes nothing.
func TestRedeemRefused(t *testing.T) {
	for _, c := range []struct {
		name   string
		now    time.Time
		change func(*memoryStorage)
	}{
		{"expired", testNow.Add(24 * time.Hour), func(*memoryStorage) {}},
		{"address changed", testNow, func(s *memoryStorage) { s.state.Email = "new@example.org" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage, service, secret := requested(t)
			c.change(storage)

			err := at(service, c.now).Redeem(t.Context(), secret)
			require.ErrorIs(t, err, ErrLinkInvalid)

			assert.False(t, storage.state.Verified)
			assert.Len(t, storage.links, 1)
		})
	}
}

// A link just before its expiry still works.
func TestRedeemBeforeExpiry(t *testing.T) {
	storage, service, secret := requested(t)

	err := at(service, testNow.Add(24*time.Hour-time.Microsecond)).Redeem(t.Context(), secret)
	require.NoError(t, err)

	assert.True(t, storage.state.Verified)
}

// A secret that is malformed or matches no link is not valid.
func TestRedeemUnknown(t *testing.T) {
	_, service, secret := requested(t)
	other, _ := newSecret()
	for _, c := range []struct {
		name   string
		secret string
	}{
		{"unknown", other},
		{"empty", ""},
		{"short", secret[:len(secret)-1]},
		{"long", secret + "A"},
		{"padded", secret[:len(secret)-1] + "="},
		{"standard base64", secret[:len(secret)-1] + "+"},
		{"line break", secret[:20] + "\n" + secret[20:len(secret)-1]},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := service.Redeem(t.Context(), c.secret)
			require.ErrorIs(t, err, ErrLinkInvalid)
		})
	}
}

func TestRedeemStorageFailure(t *testing.T) {
	storage, service, secret := requested(t)
	failure := errors.New("database unavailable")
	storage.err = failure

	err := service.Redeem(t.Context(), secret)
	require.ErrorIs(t, err, failure)
}
