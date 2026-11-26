package emailverification

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/attemptlimit"
	"amplop/internal/systememail"
)

func TestRequest(t *testing.T) {
	storage, mailer := unverified(), &fakeMailer{}

	address, err := testService(storage, mailer).Request(t.Context(), testUserID)
	require.NoError(t, err)

	assert.Equal(t, "admin@example.org", address)
	require.Len(t, mailer.sent, 1)
	sent := mailer.sent[0]
	assert.Equal(t, "email verification", sent.Purpose)
	assert.Equal(t, "admin@example.org", sent.To)
	assert.Equal(t, "Verify your email address for amplop", sent.Subject)
	assert.True(t, sent.Private, "the body holds the secret")
	assert.Contains(t, sent.Body, "Someone asked amplop at https://mail.example.com to verify this email address.")
	assert.Contains(t, sent.Body, "The link expires in 24 hours.")
	assert.Contains(t, sent.Body, "If you didn't ask for this, you can ignore this email.")

	// The link carries the secret; storage keeps only its hash.
	secret := secretOf(t, sent.Body)
	raw, err := base64.RawURLEncoding.Strict().DecodeString(secret)
	require.NoError(t, err)
	assert.Len(t, raw, 32)
	require.Len(t, storage.links, 1)
	assert.Equal(t, Link{
		TokenHash: sha256.Sum256(raw), UserID: testUserID, Email: "admin@example.org",
		CreatedAt: testNow, ExpiresAt: testNow.Add(24 * time.Hour),
	}, storage.links[0])
}

// Each request sends a new secret, and earlier links stay.
func TestRequestAgain(t *testing.T) {
	storage, mailer := unverified(), &fakeMailer{}
	service := testService(storage, mailer)

	_, err := service.Request(t.Context(), testUserID)
	require.NoError(t, err)
	_, err = service.Request(t.Context(), testUserID)
	require.NoError(t, err)

	require.Len(t, mailer.sent, 2)
	assert.NotEqual(t, secretOf(t, mailer.sent[0].Body), secretOf(t, mailer.sent[1].Body))
	require.Len(t, storage.links, 2)
	assert.NotEqual(t, storage.links[0].TokenHash, storage.links[1].TokenHash)
}

// recentLinks returns n of the user's links created age before [testNow].
func recentLinks(n int, age time.Duration) []Link {
	links := make([]Link, n)
	for i := range links {
		links[i] = sentLink(testUserID, age)
	}
	return links
}

// Refused requests save no link and send nothing. The checks run in order:
// verified, then the throttle, then the settings.
func TestRequestRefused(t *testing.T) {
	for _, c := range []struct {
		name  string
		state RequestState
		links []Link
		want  error
	}{
		{"already verified", RequestState{Email: "admin@example.org", Verified: true, MailConfigured: true}, nil, ErrAlreadyVerified},
		{"verified and throttled", RequestState{Email: "admin@example.org", Verified: true}, recentLinks(3, time.Minute), ErrAlreadyVerified},
		{"3 in 15 minutes", RequestState{Email: "admin@example.org", MailConfigured: true}, recentLinks(3, time.Minute), ErrThrottled},
		{"3 just inside 15 minutes", RequestState{Email: "admin@example.org", MailConfigured: true}, recentLinks(3, 15*time.Minute-time.Microsecond), ErrThrottled},
		{"10 in 24 hours", RequestState{Email: "admin@example.org", MailConfigured: true}, recentLinks(10, time.Hour), ErrThrottled},
		{"10 just inside 24 hours", RequestState{Email: "admin@example.org", MailConfigured: true}, recentLinks(10, 24*time.Hour-time.Microsecond), ErrThrottled},
		{"throttled without system email", RequestState{Email: "admin@example.org"}, recentLinks(3, time.Minute), ErrThrottled},
		{"no system email", RequestState{Email: "admin@example.org"}, nil, systememail.ErrNotConfigured},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage, mailer := &memoryStorage{state: c.state, links: c.links}, &fakeMailer{}

			address, err := testService(storage, mailer).Request(t.Context(), testUserID)
			require.ErrorIs(t, err, c.want)

			assert.Empty(t, address)
			assert.Len(t, storage.links, len(c.links), "no link is saved")
			assert.Empty(t, mailer.sent)
		})
	}
}

// Links that have left a window no longer count toward its limit.
func TestRequestUnderLimit(t *testing.T) {
	for _, c := range []struct {
		name  string
		links []Link
	}{
		{"2 in 15 minutes", recentLinks(2, time.Minute)},
		{"3 exactly 15 minutes ago", recentLinks(3, 15*time.Minute)},
		{"9 in 24 hours", recentLinks(9, time.Hour)},
		{"10 exactly 24 hours ago", recentLinks(10, 24*time.Hour)},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage, mailer := unverified(), &fakeMailer{}
			storage.links = c.links

			_, err := testService(storage, mailer).Request(t.Context(), testUserID)
			require.NoError(t, err)

			assert.Len(t, mailer.sent, 1)
		})
	}
}

// Deleting expired links must not lower the throttle's count.
func TestLinkLifetimeCoversThrottle(t *testing.T) {
	assert.GreaterOrEqual(t, linkLifetime, longLimit.Window)
}

// A request deletes the user's expired links and keeps live ones and other
// users' links.
func TestRequestCleanup(t *testing.T) {
	storage, mailer := unverified(), &fakeMailer{}
	live, expired, others := sentLink(testUserID, time.Hour), sentLink(testUserID, 24*time.Hour), sentLink(8, 48*time.Hour)
	storage.links = []Link{live, expired, others}

	_, err := testService(storage, mailer).Request(t.Context(), testUserID)
	require.NoError(t, err)

	require.Len(t, storage.links, 3)
	assert.Equal(t, []Link{live, others}, storage.links[:2])
}

// A failed send is returned, and the link stays saved: it counts toward the
// throttle and still works if the server delivered it.
func TestRequestSendFailed(t *testing.T) {
	for _, failure := range []error{
		&systememail.SendError{Reason: systememail.ReasonRecipient, Err: errors.New("550 No such user")},
		systememail.ErrPasswordUnreadable,
	} {
		t.Run(failure.Error(), func(t *testing.T) {
			storage, mailer := unverified(), &fakeMailer{err: failure}

			address, err := testService(storage, mailer).Request(t.Context(), testUserID)
			require.ErrorIs(t, err, failure)

			assert.Empty(t, address)
			assert.Len(t, storage.links, 1)
		})
	}
}

func TestRequestStorageFailure(t *testing.T) {
	failure := errors.New("database unavailable")
	storage, mailer := &memoryStorage{err: failure}, &fakeMailer{}

	_, err := testService(storage, mailer).Request(t.Context(), testUserID)
	require.ErrorIs(t, err, failure)

	assert.Empty(t, mailer.sent)
}

// A refusal says how long until one more link fits.
func TestRequestThrottledWait(t *testing.T) {
	storage := &memoryStorage{state: RequestState{Email: "admin@example.org", MailConfigured: true}, links: recentLinks(3, time.Minute)}

	_, err := testService(storage, &fakeMailer{}).Request(t.Context(), testUserID)

	require.ErrorIs(t, err, ErrThrottled)
	refusal, ok := errors.AsType[*attemptlimit.Error](err)
	require.True(t, ok)
	assert.Equal(t, 14*time.Minute, refusal.Wait)
}
