package emailverification

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/systememail"
)

const testUserID = int64(7)

var testNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// memoryStorage keeps one user's state and links like the database does.
type memoryStorage struct {
	state RequestState
	links []Link
	err   error
}

func (m *memoryStorage) RequestEmailVerification(_ context.Context, userID int64, create func(RequestState) (RequestChange, error)) error {
	if m.err != nil {
		return m.err
	}
	state := m.state
	state.LinksCreatedAt = nil
	for _, link := range m.links {
		if link.UserID == userID {
			state.LinksCreatedAt = append(state.LinksCreatedAt, link.CreatedAt)
		}
	}
	change, err := create(state)
	if err != nil {
		return err
	}
	kept := m.links[:0:0]
	for _, link := range m.links {
		if link.UserID != userID || link.ExpiresAt.After(change.ExpiredBy) {
			kept = append(kept, link)
		}
	}
	m.links = append(kept, change.Link)
	return nil
}

// fakeMailer records each message and answers with err.
type fakeMailer struct {
	err  error
	sent []systememail.Outgoing
}

func (f *fakeMailer) Send(_ context.Context, outgoing systememail.Outgoing) (int64, error) {
	f.sent = append(f.sent, outgoing)
	return 1, f.err
}

func unverified() *memoryStorage {
	return &memoryStorage{state: RequestState{Email: "Admin@example.org", MailConfigured: true}}
}

func testService(storage *memoryStorage, mailer *fakeMailer) *Service {
	publicURL := &url.URL{Scheme: "https", Host: "mail.example.com"}
	return NewService(storage, mailer, publicURL, func() time.Time { return testNow })
}

// sentLink returns the link at age before testNow, for userID.
func sentLink(userID int64, age time.Duration) Link {
	createdAt := testNow.Add(-age)
	return Link{UserID: userID, Address: "Admin@example.org", CreatedAt: createdAt, ExpiresAt: createdAt.Add(linkLifetime)}
}

// secretOf returns the secret in a verification email's link.
func secretOf(t *testing.T, body string) string {
	t.Helper()

	_, after, ok := strings.Cut(body, "https://mail.example.com/verify-email#")
	require.True(t, ok, "the body has a link")
	secret, _, _ := strings.Cut(after, "\n")
	return secret
}

func TestRequest(t *testing.T) {
	storage, mailer := unverified(), &fakeMailer{}

	address, err := testService(storage, mailer).Request(t.Context(), testUserID)
	require.NoError(t, err)

	assert.Equal(t, "Admin@example.org", address)
	require.Len(t, mailer.sent, 1)
	sent := mailer.sent[0]
	assert.Equal(t, "email verification", sent.Purpose)
	assert.Equal(t, "Admin@example.org", sent.To)
	assert.Equal(t, "Verify your email address for inbok", sent.Subject)
	assert.Contains(t, sent.Body, "Someone asked inbok at https://mail.example.com to verify this email address.")
	assert.Contains(t, sent.Body, "The link expires in 24 hours.")
	assert.Contains(t, sent.Body, "If you didn't ask for this, you can ignore this email.")

	// The link carries the secret; storage keeps only its hash.
	secret := secretOf(t, sent.Body)
	raw, err := base64.RawURLEncoding.Strict().DecodeString(secret)
	require.NoError(t, err)
	assert.Len(t, raw, secretSize)
	require.Len(t, storage.links, 1)
	assert.Equal(t, Link{
		TokenHash: sha256.Sum256(raw), UserID: testUserID, Address: "Admin@example.org",
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

// recentLinks returns n of the user's links created age before testNow.
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
		{"already verified", RequestState{Email: "Admin@example.org", Verified: true, MailConfigured: true}, nil, ErrAlreadyVerified},
		{"verified and throttled", RequestState{Email: "Admin@example.org", Verified: true}, recentLinks(3, time.Minute), ErrAlreadyVerified},
		{"3 in 15 minutes", RequestState{Email: "Admin@example.org", MailConfigured: true}, recentLinks(3, time.Minute), ErrThrottled},
		{"3 just inside 15 minutes", RequestState{Email: "Admin@example.org", MailConfigured: true}, recentLinks(3, 15*time.Minute-time.Microsecond), ErrThrottled},
		{"10 in 24 hours", RequestState{Email: "Admin@example.org", MailConfigured: true}, recentLinks(10, time.Hour), ErrThrottled},
		{"10 just inside 24 hours", RequestState{Email: "Admin@example.org", MailConfigured: true}, recentLinks(10, 24*time.Hour-time.Microsecond), ErrThrottled},
		{"throttled without system email", RequestState{Email: "Admin@example.org"}, recentLinks(3, time.Minute), ErrThrottled},
		{"no system email", RequestState{Email: "Admin@example.org"}, nil, systememail.ErrNotConfigured},
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
	assert.GreaterOrEqual(t, linkLifetime, longWindow)
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
