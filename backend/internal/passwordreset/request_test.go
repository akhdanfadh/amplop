package passwordreset

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/attemptlimit"
	"inbok/internal/emailaddress"
)

var clientAddr = netip.MustParseAddr("192.0.2.1")

// requestAttempt is one attempt [requestMemory] keeps.
type requestAttempt struct {
	id        int64
	client    string
	userID    int64     // 0: no link was saved
	linkedAt  time.Time // zero: no link was saved
	createdAt time.Time
}

// linkJob is a reset link job [requestMemory] saved: the attempt, and the ID
// of the account with the requested email, 0 for none.
type linkJob struct {
	attemptID int64
	userID    int64
}

// requestMemory keeps accounts, attempts, jobs, and links like the database
// does.
type requestMemory struct {
	accounts  []Account
	attempts  []requestAttempt
	jobs      []linkJob
	links     []Link
	since     []time.Time
	err       error // returned by RecordPasswordResetRequest
	createErr error // returned by CreatePasswordResetLink
	lastID    int64
}

// RecordPasswordResetRequest calls check with client's attempt times after
// since, records an attempt at the time check returns, and saves its job for
// the account with email, if any.
func (m *requestMemory) RecordPasswordResetRequest(_ context.Context, client, email string, since time.Time, check func([]time.Time) (time.Time, error)) error {
	if m.err != nil {
		return m.err
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
		return err
	}
	m.lastID++
	m.attempts = append(m.attempts, requestAttempt{id: m.lastID, client: client, createdAt: createdAt})
	var userID int64
	if at := slices.IndexFunc(m.accounts, func(a Account) bool { return a.User.Email == email }); at >= 0 {
		userID = m.accounts[at].User.ID
	}
	m.jobs = append(m.jobs, linkJob{attemptID: m.lastID, userID: userID})
	return nil
}

// CreatePasswordResetLink returns createErr, or calls create with the
// account with userID and the times links were saved for it after since,
// and saves the link create returns.
func (m *requestMemory) CreatePasswordResetLink(_ context.Context, attemptID, userID int64, since time.Time, create func(LinkState) (LinkChange, error)) error {
	if m.createErr != nil {
		return m.createErr
	}
	var state LinkState
	if at := slices.IndexFunc(m.accounts, func(a Account) bool { return a.User.ID == userID }); at >= 0 {
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

// testRequests returns [Requests] over storage whose clock reads [testNow].
func testRequests(storage *requestMemory) *Requests {
	return NewRequests(storage, func() time.Time { return testNow })
}

// A request records the attempt and saves its job for the account with the
// email, as stored, whatever the request's spelling; it sends nothing
// itself.
func TestRequestRecords(t *testing.T) {
	storage := &requestMemory{accounts: []Account{activeAccount()}}

	err := testRequests(storage).Request(t.Context(), " Member@Example.com ", clientAddr)

	require.NoError(t, err)
	assert.Equal(t, []requestAttempt{{id: 1, client: "192.0.2.1/32", createdAt: testNow}}, storage.attempts)
	assert.Equal(t, []linkJob{{attemptID: 1, userID: testUser.ID}}, storage.jobs)
	assert.Empty(t, storage.links)
	// Attempts count for the longest window.
	assert.Equal(t, []time.Time{testNow.Add(-24 * time.Hour)}, storage.since)
}

// An email no account has gets the same answer, attempt, and job, with no
// account.
func TestRequestUnknownEmail(t *testing.T) {
	storage := &requestMemory{}

	err := testRequests(storage).Request(t.Context(), "nobody@example.com", clientAddr)

	require.NoError(t, err)
	assert.Equal(t, []requestAttempt{{id: 1, client: "192.0.2.1/32", createdAt: testNow}}, storage.attempts)
	assert.Equal(t, []linkJob{{attemptID: 1}}, storage.jobs)
}

// A client over its limit is refused with how long to wait, whatever the
// email, and nothing is recorded.
func TestRequestThrottled(t *testing.T) {
	var attempts []requestAttempt
	for i := range 10 {
		attempts = append(attempts, requestAttempt{id: int64(i + 1), client: "2001:db8:1:2::/64", createdAt: testNow.Add(-10 * time.Minute)})
	}
	storage := &requestMemory{accounts: []Account{activeAccount()}, attempts: attempts}

	// Another address in the same /64 is the same client.
	err := testRequests(storage).Request(t.Context(), testUser.Email, netip.MustParseAddr("2001:db8:1:2:ffff::9"))

	require.ErrorIs(t, err, ErrThrottled)
	throttled, ok := errors.AsType[*attemptlimit.Error](err)
	require.True(t, ok)
	assert.Equal(t, 5*time.Minute, throttled.Wait)
	assert.Len(t, storage.attempts, 10)
	assert.Empty(t, storage.jobs)
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
			storage := &requestMemory{accounts: []Account{activeAccount()}}

			err := testRequests(storage).Request(t.Context(), c.email, c.client)

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, storage.attempts)
			assert.Empty(t, storage.jobs)
		})
	}
}

// A storage failure while recording the attempt is returned.
func TestRequestStorageError(t *testing.T) {
	storage := &requestMemory{err: errors.New("database down")}

	err := testRequests(storage).Request(t.Context(), testUser.Email, clientAddr)

	require.ErrorIs(t, err, storage.err)
}
