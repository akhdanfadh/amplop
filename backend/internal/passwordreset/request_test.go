package passwordreset

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/attemptlimit"
	"inbok/internal/emailaddress"
	"inbok/internal/systememail"
)

var clientAddr = netip.MustParseAddr("192.0.2.1")

// requestAttempt is one attempt requestMemory keeps.
type requestAttempt struct {
	id        int64
	client    string
	userID    int64     // 0: no link was saved
	linkedAt  time.Time // zero: no link was saved
	createdAt time.Time
}

// requestMemory keeps accounts, attempts, and links like the database does.
type requestMemory struct {
	mu        sync.Mutex
	accounts  []Account
	attempts  []requestAttempt
	links     []Link
	since     []time.Time
	err       error // returned by RecordPasswordResetRequest
	createErr error // returned by CreatePasswordResetLink
	lastID    int64

	// creating, when set, is received from before CreatePasswordResetLink
	// reads, so a test can hold the background work.
	creating chan struct{}
}

// RecordPasswordResetRequest drops attempts at or before since, calls check
// with client's attempt times, and records an attempt at the time check
// returns.
func (m *requestMemory) RecordPasswordResetRequest(_ context.Context, client string, since time.Time, check func([]time.Time) (time.Time, error)) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return 0, m.err
	}
	m.since = append(m.since, since)
	var times []time.Time
	for _, attempt := range m.attempts {
		if attempt.client == client && attempt.createdAt.After(since) {
			times = append(times, attempt.createdAt)
		}
	}
	createdAt, err := check(times)
	if err != nil {
		return 0, err
	}
	m.lastID++
	m.attempts = append(m.attempts, requestAttempt{id: m.lastID, client: client, createdAt: createdAt})
	return m.lastID, nil
}

// CreatePasswordResetLink returns createErr, or calls create with the
// account with email and the times links were saved for it after since, and
// saves the link create returns.
func (m *requestMemory) CreatePasswordResetLink(_ context.Context, attemptID int64, email string, since time.Time, create func(LinkState) (LinkChange, error)) error {
	if m.creating != nil {
		<-m.creating
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return m.createErr
	}
	var state LinkState
	if at := slices.IndexFunc(m.accounts, func(a Account) bool { return a.User.Email == email }); at >= 0 {
		found := m.accounts[at]
		state.Account = &found
		for _, attempt := range m.attempts {
			if attempt.userID == found.User.ID && attempt.linkedAt.After(since) {
				state.LinksSent = append(state.LinksSent, attempt.linkedAt)
			}
		}
	}
	change, err := create(state)
	if err != nil {
		return err
	}
	if change.Link != nil {
		m.links = append(m.links, *change.Link)
		// The attempt counts its first link; a later one, or one whose
		// attempt is gone, gets a row of its own with no client.
		linked := change.Link.CreatedAt
		at := slices.IndexFunc(m.attempts, func(a requestAttempt) bool { return a.id == attemptID })
		if at >= 0 && m.attempts[at].linkedAt.IsZero() {
			m.attempts[at].userID, m.attempts[at].linkedAt = state.Account.User.ID, linked
		} else {
			m.lastID++
			m.attempts = append(m.attempts, requestAttempt{id: m.lastID, userID: state.Account.User.ID, linkedAt: linked, createdAt: linked})
		}
	}
	return nil
}

// activeAccount is testUser, verified and active.
func activeAccount() Account {
	return Account{User: testUser, Verified: true}
}

// testRequests returns Requests over storage and mailer whose clock reads
// testNow, and the Background it sends with, logging to logger.
func testRequests(t *testing.T, storage *requestMemory, mailer *fakeMailer, logger *slog.Logger) (*Requests, *Background) {
	t.Helper()
	background := testBackground(t, logger)
	return NewRequests(storage, mailer, testPublicURL, background, func() time.Time { return testNow }), background
}

// linksSent returns n attempts by another client whose links were saved for
// testUser age before testNow, a minute after their requests.
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

func TestRequestLink(t *testing.T) {
	storage, mailer := &requestMemory{accounts: []Account{activeAccount()}}, &fakeMailer{}
	requests, background := testRequests(t, storage, mailer, slog.New(slog.DiscardHandler))

	err := requests.Request(t.Context(), " Member@Example.com ", clientAddr)
	require.NoError(t, err)
	require.NoError(t, background.Wait(t.Context()))

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

	// Attempts count for the longest window.
	assert.Equal(t, []time.Time{testNow.Add(-24 * time.Hour)}, storage.since)
}

// Every address gets the same answer and the same attempt; only an active,
// verified account under its limits gets a link.
func TestRequestSameAnswer(t *testing.T) {
	unverified, deactivated := activeAccount(), activeAccount()
	unverified.Verified, deactivated.Deactivated = false, true
	cases := []struct {
		name     string
		accounts []Account
		sent     []requestAttempt
		wantLink bool
	}{
		{name: "unknown"},
		{name: "unverified", accounts: []Account{unverified}},
		{name: "deactivated", accounts: []Account{deactivated}},
		{name: "hourly limit", accounts: []Account{activeAccount()}, sent: linksSent(3, 59*time.Minute)},
		{name: "daily limit", accounts: []Account{activeAccount()}, sent: append(linksSent(2, 30*time.Minute), linksSent(3, 23*time.Hour)...)},
		{name: "eligible", accounts: []Account{activeAccount()}, wantLink: true},
		{name: "links aged out", accounts: []Account{activeAccount()}, sent: append(linksSent(3, time.Hour), linksSent(2, 24*time.Hour)...), wantLink: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage, mailer := &requestMemory{accounts: c.accounts, attempts: slices.Clone(c.sent)}, &fakeMailer{}
			requests, background := testRequests(t, storage, mailer, slog.New(slog.DiscardHandler))

			err := requests.Request(t.Context(), testUser.Email, clientAddr)
			require.NoError(t, background.Wait(t.Context()))

			require.NoError(t, err)
			recorded := storage.attempts[len(storage.attempts)-1]
			assert.Equal(t, "192.0.2.1/32", recorded.client)
			if c.wantLink {
				assert.Len(t, storage.links, 1)
				assert.Len(t, mailer.sent, 1)
				assert.Equal(t, testUser.ID, recorded.userID)
			} else {
				assert.Empty(t, storage.links)
				assert.Empty(t, mailer.sent)
				assert.Zero(t, recorded.userID)
			}
		})
	}
}

// Request answers before the account is read: the background work waits
// here until the test lets it go on.
func TestRequestAnswersFirst(t *testing.T) {
	storage := &requestMemory{accounts: []Account{activeAccount()}, creating: make(chan struct{})}
	mailer := &fakeMailer{}
	requests, background := testRequests(t, storage, mailer, slog.New(slog.DiscardHandler))

	err := requests.Request(t.Context(), testUser.Email, clientAddr)
	require.NoError(t, err)
	sentBefore := len(mailer.sent)
	close(storage.creating)
	require.NoError(t, background.Wait(t.Context()))

	assert.Zero(t, sentBefore)
	assert.Len(t, mailer.sent, 1)
}

// A client over its limit is refused with how long to wait, whatever the
// email, and nothing is recorded or sent.
func TestRequestThrottled(t *testing.T) {
	var attempts []requestAttempt
	for i := range 10 {
		attempts = append(attempts, requestAttempt{id: int64(i + 1), client: "2001:db8:1:2::/64", createdAt: testNow.Add(-10 * time.Minute)})
	}
	storage, mailer := &requestMemory{accounts: []Account{activeAccount()}, attempts: attempts}, &fakeMailer{}
	requests, background := testRequests(t, storage, mailer, slog.New(slog.DiscardHandler))

	// Another address in the same /64 is the same client.
	err := requests.Request(t.Context(), testUser.Email, netip.MustParseAddr("2001:db8:1:2:ffff::9"))
	require.NoError(t, background.Wait(t.Context()))

	require.ErrorIs(t, err, ErrThrottled)
	throttled, ok := errors.AsType[*attemptlimit.Error](err)
	require.True(t, ok)
	assert.Equal(t, 5*time.Minute, throttled.Wait)
	assert.Len(t, storage.attempts, 10)
	assert.Empty(t, mailer.sent)
}

func TestRequestRefusesInput(t *testing.T) {
	cases := []struct {
		name   string
		email  string
		client netip.Addr
		want   error
	}{
		{"invalid email", "not an email", clientAddr, emailaddress.ErrInvalid},
		{"no client", testUser.Email, netip.Addr{}, errClientAddr},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage, mailer := &requestMemory{accounts: []Account{activeAccount()}}, &fakeMailer{}
			requests, background := testRequests(t, storage, mailer, slog.New(slog.DiscardHandler))

			err := requests.Request(t.Context(), c.email, c.client)
			require.NoError(t, background.Wait(t.Context()))

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, storage.attempts)
			assert.Empty(t, mailer.sent)
		})
	}
}

// A storage failure while recording the attempt is returned.
func TestRequestStorageError(t *testing.T) {
	storage := &requestMemory{err: errors.New("database down")}
	requests, background := testRequests(t, storage, &fakeMailer{}, slog.New(slog.DiscardHandler))

	err := requests.Request(t.Context(), testUser.Email, clientAddr)
	require.NoError(t, background.Wait(t.Context()))

	require.ErrorIs(t, err, storage.err)
}

// A failed send is logged without the secret and keeps its link, since the
// message may still arrive.
func TestRequestSendFails(t *testing.T) {
	logger, out := logBuffer()
	storage := &requestMemory{accounts: []Account{activeAccount()}}
	mailer := &fakeMailer{err: systememail.ErrNotConfigured}
	requests, background := testRequests(t, storage, mailer, logger)

	err := requests.Request(t.Context(), testUser.Email, clientAddr)
	require.NoError(t, background.Wait(t.Context()))

	require.NoError(t, err)
	assert.Len(t, storage.links, 1)
	assert.Contains(t, out.String(), `purpose="password reset link"`)
	assert.Contains(t, out.String(), "system email is not configured")
	require.Len(t, mailer.sent, 1)
	assert.NotContains(t, out.String(), secretOf(t, mailer.sent[0].Body))
}

// The link is saved for, and sent to, the stored email, whatever the
// request's spelling.
func TestRequestStoredEmail(t *testing.T) {
	storage, mailer := &requestMemory{accounts: []Account{{User: account.User{ID: 9, Email: "member@example.com"}, Verified: true}}}, &fakeMailer{}
	requests, background := testRequests(t, storage, mailer, slog.New(slog.DiscardHandler))

	err := requests.Request(t.Context(), "MEMBER@example.COM", clientAddr)
	require.NoError(t, background.Wait(t.Context()))

	require.NoError(t, err)
	require.Len(t, mailer.sent, 1)
	require.Len(t, storage.links, 1)
	assert.Equal(t, "member@example.com", mailer.sent[0].To)
	assert.Equal(t, "member@example.com", storage.links[0].Email)
}

// The account limits count when links were saved, not when their requests
// came: three links saved 59 minutes ago, after waiting a minute or more,
// still hold the hourly limit.
func TestRequestLimitsCountLinkTime(t *testing.T) {
	storage := &requestMemory{accounts: []Account{activeAccount()}, attempts: linksSent(3, 59*time.Minute)}
	for i := range storage.attempts {
		storage.attempts[i].createdAt = testNow.Add(-61 * time.Minute)
	}
	mailer := &fakeMailer{}
	requests, background := testRequests(t, storage, mailer, slog.New(slog.DiscardHandler))

	err := requests.Request(t.Context(), testUser.Email, clientAddr)
	require.NoError(t, background.Wait(t.Context()))

	require.NoError(t, err)
	assert.Empty(t, mailer.sent)
}

// A storage failure in the background step sends nothing and is logged.
func TestRequestLinkStorageError(t *testing.T) {
	logger, out := logBuffer()
	storage := &requestMemory{accounts: []Account{activeAccount()}, createErr: errors.New("database down")}
	mailer := &fakeMailer{}
	requests, background := testRequests(t, storage, mailer, logger)

	err := requests.Request(t.Context(), testUser.Email, clientAddr)
	require.NoError(t, background.Wait(t.Context()))

	require.NoError(t, err, "the answer came before the failure")
	assert.Empty(t, mailer.sent)
	assert.Contains(t, out.String(), `msg="password reset work failed" purpose="password reset link" err="database down"`)
}
