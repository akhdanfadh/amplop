package passwordreset

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/emailaddress"
	"inbok/internal/systememail"
)

// activeAccount is [testUser], verified and active.
func activeAccount() Account {
	return Account{User: testUser, Verified: true}
}

// testLinks returns [Links] over storage and mailer whose clock reads
// [testNow].
func testLinks(storage *requestMemory, mailer *fakeMailer) *Links {
	return NewLinks(storage, mailer, testPublicURL, func() time.Time { return testNow })
}

// linksSent returns n attempts by another client whose links were saved for
// [testUser] age before [testNow], a minute after their requests.
func linksSent(n int, age time.Duration) []requestAttempt {
	made := make([]requestAttempt, n)
	for i := range made {
		linkedAt := testNow.Add(-age)
		made[i] = requestAttempt{
			id: int64(100 + i), client: "198.51.100.9/32", userID: testUser.ID,
			linkedAt: linkedAt, createdAt: linkedAt.Add(-time.Minute),
		}
	}
	return made
}

// secretOf returns the secret in a reset link message's body.
func secretOf(t *testing.T, body string) string {
	t.Helper()

	_, after, ok := strings.Cut(body, "https://mail.example.com/reset-password#")
	require.True(t, ok, "the body has a link")
	secret, _, _ := strings.Cut(after, "\n")
	return secret
}

// recorded returns storage holding one recorded attempt, ID 1, and accounts.
func recorded(accounts ...Account) *requestMemory {
	return &requestMemory{
		accounts: accounts,
		attempts: []requestAttempt{{id: 1, client: "192.0.2.1/32", createdAt: testNow}},
	}
}

func TestLinkSent(t *testing.T) {
	storage, mailer := recorded(activeAccount()), &fakeMailer{}

	outcome, err := testLinks(storage, mailer).Send(t.Context(), 1, testUser.ID)

	require.NoError(t, err)
	assert.Equal(t, OutcomeSent, outcome)
	require.Len(t, mailer.sent, 1)
	sent := mailer.sent[0]
	assert.Equal(t, "password reset link", sent.Purpose)
	assert.Equal(t, testUser.Email, sent.To)
	assert.Equal(t, "Reset your inbok password", sent.Subject)
	assert.True(t, sent.Private, "the body holds the secret")
	assert.Contains(t, sent.Body, "Someone asked to reset the password of your inbok account at https://mail.example.com.")
	assert.Contains(t, sent.Body, "The link expires in 1 hour.")
	assert.Contains(t, sent.Body, "Your password stays the same unless the link is used.")

	// The link carries the secret; storage keeps only its hash.
	raw, err := base64.RawURLEncoding.Strict().DecodeString(secretOf(t, sent.Body))
	require.NoError(t, err)
	assert.Equal(t, []Link{{
		TokenHash: sha256.Sum256(raw), UserID: testUser.ID, Email: testUser.Email,
		CreatedAt: testNow, ExpiresAt: testNow.Add(time.Hour),
	}}, storage.links)
	assert.Equal(t, []requestAttempt{{id: 1, client: "192.0.2.1/32", userID: testUser.ID, linkedAt: testNow, createdAt: testNow}}, storage.attempts)
}

// Only an active, verified account under its limits gets a link; the rest
// are skipped without an error.
func TestLinkSkipped(t *testing.T) {
	unverified, deactivated := activeAccount(), activeAccount()
	unverified.Verified, deactivated.Deactivated = false, true
	cases := []struct {
		name     string
		accounts []Account
		userID   int64
		sent     []requestAttempt
		want     Outcome
	}{
		{name: "no account", want: OutcomeNotEligible},
		{name: "account gone", userID: testUser.ID, want: OutcomeNotEligible},
		{name: "unverified", accounts: []Account{unverified}, userID: testUser.ID, want: OutcomeNotEligible},
		{name: "deactivated", accounts: []Account{deactivated}, userID: testUser.ID, want: OutcomeNotEligible},
		{name: "hourly limit", accounts: []Account{activeAccount()}, userID: testUser.ID, sent: linksSent(3, 59*time.Minute), want: OutcomeNotEligible},
		{name: "daily limit", accounts: []Account{activeAccount()}, userID: testUser.ID, sent: append(linksSent(2, 30*time.Minute), linksSent(3, 23*time.Hour)...), want: OutcomeNotEligible},
		{name: "eligible", accounts: []Account{activeAccount()}, userID: testUser.ID, want: OutcomeSent},
		{name: "links aged out", accounts: []Account{activeAccount()}, userID: testUser.ID, sent: append(linksSent(3, time.Hour), linksSent(2, 24*time.Hour)...), want: OutcomeSent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage, mailer := recorded(c.accounts...), &fakeMailer{}
			storage.attempts = append(storage.attempts, slices.Clone(c.sent)...)

			outcome, err := testLinks(storage, mailer).Send(t.Context(), 1, c.userID)

			require.NoError(t, err)
			assert.Equal(t, c.want, outcome)
			if c.want == OutcomeSent {
				assert.Len(t, storage.links, 1)
				assert.Len(t, mailer.sent, 1)
			} else {
				assert.Empty(t, storage.links)
				assert.Empty(t, mailer.sent)
				assert.Zero(t, storage.attempts[0].userID)
			}
		})
	}
}

// The link is saved for, and sent to, the stored email.
func TestLinkStoredEmail(t *testing.T) {
	storage, mailer := recorded(Account{User: account.User{ID: 9, Email: "member@example.com"}, Verified: true}), &fakeMailer{}

	_, err := testLinks(storage, mailer).Send(t.Context(), 1, 9)

	require.NoError(t, err)
	require.Len(t, mailer.sent, 1)
	require.Len(t, storage.links, 1)
	assert.Equal(t, "member@example.com", mailer.sent[0].To)
	assert.Equal(t, "member@example.com", storage.links[0].Email)
}

// The account limits count when links were saved, not when their requests
// came: three links saved 59 minutes ago, after waiting a minute or more,
// still hold the hourly limit.
func TestLinkLimitsCountLinkTime(t *testing.T) {
	storage := recorded(activeAccount())
	sent := linksSent(3, 59*time.Minute)
	for i := range sent {
		sent[i].createdAt = testNow.Add(-61 * time.Minute)
	}
	storage.attempts = append(storage.attempts, sent...)
	mailer := &fakeMailer{}

	outcome, err := testLinks(storage, mailer).Send(t.Context(), 1, testUser.ID)

	require.NoError(t, err)
	assert.Equal(t, OutcomeNotEligible, outcome)
	assert.Empty(t, mailer.sent)
}

// A send that may pass later is returned as [ErrLinkNotSent], naming only
// where it failed: the server's reply can quote the live link and the
// address, and DBOS logs and keeps the error. It keeps its link, since the
// message may still arrive, and a retry saves and counts another.
func TestLinkSendFails(t *testing.T) {
	storage := recorded(activeAccount())
	mailer := &fakeMailer{}
	links := testLinks(storage, mailer)
	mailer.err = &systememail.SendError{
		Reason: systememail.ReasonTimeout, Unconfirmed: true,
		Err: errors.New("451 4.3.0 message for member@example.com held: https://mail.example.com/reset-password#quoted"),
	}

	_, err := links.Send(t.Context(), 1, testUser.ID)
	require.ErrorIs(t, err, ErrLinkNotSent)
	require.EqualError(t, err, "password reset link not sent: failed at timeout, unconfirmed")
	assert.Len(t, storage.links, 1)

	mailer.err = nil
	outcome, err := links.Send(t.Context(), 1, testUser.ID)
	require.NoError(t, err)
	assert.Equal(t, OutcomeSent, outcome)
	assert.Len(t, storage.links, 2)
	assert.NotEqual(t, secretOf(t, mailer.sent[0].Body), secretOf(t, mailer.sent[1].Body))
	var counted int
	for _, attempt := range storage.attempts {
		if attempt.userID == testUser.ID {
			counted++
		}
	}
	assert.Equal(t, 2, counted, "each link counts toward the account's limits")
}

// A send that retrying cannot fix ends the job with its outcome instead of
// an error. Its link stays saved, unsent.
func TestLinkSendCannotPass(t *testing.T) {
	refused := &systememail.SendError{Reason: systememail.ReasonRecipient, Permanent: true, Err: errors.New("550 5.1.1 no such user")}
	cases := []struct {
		name string
		err  error
		want Outcome
	}{
		{"not configured", systememail.ErrNotConfigured, OutcomeNotConfigured},
		{"password unreadable", systememail.ErrPasswordUnreadable, OutcomeUnsendable},
		{"invalid address", emailaddress.ErrInvalid, OutcomeUnsendable},
		{"recipient refused for good", refused, OutcomeRefused},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage, mailer := recorded(activeAccount()), &fakeMailer{err: c.err}

			outcome, err := testLinks(storage, mailer).Send(t.Context(), 1, testUser.ID)

			require.NoError(t, err)
			assert.Equal(t, c.want, outcome)
		})
	}
}

// A retry counts the link its failed try saved, so an account one link
// short of its hourly limit gets no second one: the retry is skipped.
func TestLinkRetryAtLimit(t *testing.T) {
	storage := recorded(activeAccount())
	storage.attempts = append(storage.attempts, linksSent(2, 30*time.Minute)...)
	mailer := &fakeMailer{err: &systememail.SendError{Reason: systememail.ReasonConnect, Err: errors.New("connection refused")}}
	links := testLinks(storage, mailer)

	_, err := links.Send(t.Context(), 1, testUser.ID)
	require.ErrorIs(t, err, ErrLinkNotSent)
	mailer.err = nil
	outcome, err := links.Send(t.Context(), 1, testUser.ID)

	require.NoError(t, err)
	assert.Equal(t, OutcomeNotEligible, outcome)
	assert.Len(t, storage.links, 1)
}

// A storage failure sends nothing and is returned.
func TestLinkStorageError(t *testing.T) {
	storage := recorded(activeAccount())
	storage.createErr = errors.New("database down")
	mailer := &fakeMailer{}

	_, err := testLinks(storage, mailer).Send(t.Context(), 1, testUser.ID)

	require.ErrorIs(t, err, storage.createErr)
	assert.Empty(t, mailer.sent)
}
