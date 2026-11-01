package postgres

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/emailverification"
)

var verificationNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// verificationUser adds a user with email and returns their ID.
func verificationUser(t *testing.T, conn *pgx.Conn, email string) int64 {
	t.Helper()

	var id int64
	require.NoError(t, conn.QueryRow(t.Context(),
		"INSERT INTO users (email, password_hash, role) VALUES ($1, 'unused', 'member') RETURNING id", email).Scan(&id))
	return id
}

// verificationSystemEmail saves settings that need no sign-in.
func verificationSystemEmail(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	_, err := conn.Exec(t.Context(), `INSERT INTO system_email (host, port, security, from_address, from_name)
		VALUES ('localhost', 1025, 'none', 'inbok@localhost', 'inbok')`)
	require.NoError(t, err)
}

// verificationLink is a link for userID named by seed, created age before
// verificationNow.
func verificationLink(userID int64, seed string, age time.Duration) emailverification.Link {
	createdAt := verificationNow.Add(-age)
	return emailverification.Link{
		TokenHash: sha256.Sum256([]byte(seed)), UserID: userID, Address: "user@example.com",
		CreatedAt: createdAt, ExpiresAt: createdAt.Add(24 * time.Hour),
	}
}

// insertVerificationLink saves link directly.
func insertVerificationLink(t *testing.T, conn *pgx.Conn, link emailverification.Link) {
	t.Helper()

	_, err := conn.Exec(t.Context(), `INSERT INTO email_verification_links (token_hash, user_id, address, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, link.TokenHash[:], link.UserID, link.Address, link.CreatedAt, link.ExpiresAt)
	require.NoError(t, err)
}

// savedVerificationLinks lists every saved link, oldest first.
func savedVerificationLinks(t *testing.T, conn *pgx.Conn) []emailverification.Link {
	t.Helper()

	rows, err := conn.Query(t.Context(), "SELECT token_hash, user_id, address, created_at, expires_at FROM email_verification_links ORDER BY created_at, user_id")
	require.NoError(t, err)
	links, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (emailverification.Link, error) {
		var link emailverification.Link
		var hash []byte
		err := row.Scan(&hash, &link.UserID, &link.Address, &link.CreatedAt, &link.ExpiresAt)
		copy(link.TokenHash[:], hash)
		link.CreatedAt, link.ExpiresAt = link.CreatedAt.UTC(), link.ExpiresAt.UTC()
		return link, err
	})
	require.NoError(t, err)
	return links
}

// requestRecording returns a create function that records the state it reads
// and saves link, deleting links that expired by verificationNow.
func requestRecording(seen *emailverification.RequestState, link emailverification.Link) func(emailverification.RequestState) (emailverification.RequestChange, error) {
	return func(state emailverification.RequestState) (emailverification.RequestChange, error) {
		*seen = state
		return emailverification.RequestChange{Link: link, ExpiredBy: verificationNow}, nil
	}
}

// A request reads the user's state, deletes only their expired links, and
// saves the new one.
func TestEmailVerificationRequest(t *testing.T) {
	db, conn := setupStorage(t)
	user := verificationUser(t, conn, "User@example.com")
	other := verificationUser(t, conn, "other@example.com")
	verificationSystemEmail(t, conn)
	expired, live := verificationLink(user, "expired", 24*time.Hour), verificationLink(user, "live", time.Hour)
	othersExpired := verificationLink(other, "other", 48*time.Hour)
	for _, link := range []emailverification.Link{expired, live, othersExpired} {
		insertVerificationLink(t, conn, link)
	}
	added := verificationLink(user, "added", 0)
	var seen emailverification.RequestState

	err := db.RequestEmailVerification(t.Context(), user, requestRecording(&seen, added))
	require.NoError(t, err)

	assert.Equal(t, "User@example.com", seen.Email)
	assert.False(t, seen.Verified)
	assert.True(t, seen.MailConfigured)
	assert.ElementsMatch(t, []time.Time{expired.CreatedAt, live.CreatedAt}, utcTimes(seen.LinksCreatedAt))
	assert.Equal(t, []emailverification.Link{othersExpired, live, added}, savedVerificationLinks(t, conn))
}

func TestEmailVerificationRequestState(t *testing.T) {
	db, conn := setupStorage(t)
	user := verificationUser(t, conn, "user@example.com")
	_, err := conn.Exec(t.Context(), "UPDATE users SET email_verified_at = now() WHERE id = $1", user)
	require.NoError(t, err)
	var seen emailverification.RequestState

	err = db.RequestEmailVerification(t.Context(), user, requestRecording(&seen, verificationLink(user, "added", 0)))
	require.NoError(t, err)

	assert.True(t, seen.Verified)
	assert.False(t, seen.MailConfigured, "no system email is saved")
	assert.Empty(t, seen.LinksCreatedAt)
}

// A refused request changes nothing, not even expired links.
func TestEmailVerificationRequestRefused(t *testing.T) {
	db, conn := setupStorage(t)
	user := verificationUser(t, conn, "user@example.com")
	expired := verificationLink(user, "expired", 48*time.Hour)
	insertVerificationLink(t, conn, expired)

	err := db.RequestEmailVerification(t.Context(), user, func(emailverification.RequestState) (emailverification.RequestChange, error) {
		return emailverification.RequestChange{}, emailverification.ErrThrottled
	})
	require.ErrorIs(t, err, emailverification.ErrThrottled)

	assert.Equal(t, []emailverification.Link{expired}, savedVerificationLinks(t, conn))
}

func TestEmailVerificationRequestMissingUser(t *testing.T) {
	db, _ := setupStorage(t)
	called := false

	err := db.RequestEmailVerification(t.Context(), 404, func(emailverification.RequestState) (emailverification.RequestChange, error) {
		called = true
		return emailverification.RequestChange{}, nil
	})
	require.ErrorIs(t, err, account.ErrUserNotFound)

	assert.False(t, called)
}

// Requests for one user wait for each other, so the second counts the
// first's link and parallel requests cannot pass the throttle.
func TestEmailVerificationRequestSerializes(t *testing.T) {
	db, conn := setupStorage(t)
	user := verificationUser(t, conn, "user@example.com")
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()
	ready, release := make(chan struct{}), make(chan struct{})
	releaseFirst := sync.OnceFunc(func() { close(release) })
	defer releaseFirst()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	first := verificationLink(user, "first", time.Minute)
	var seen emailverification.RequestState

	// Given a first request holding the user's lock.
	go func() {
		firstDone <- db.RequestEmailVerification(ctx, user, func(emailverification.RequestState) (emailverification.RequestChange, error) {
			close(ready)
			select {
			case <-release:
				return emailverification.RequestChange{Link: first, ExpiredBy: verificationNow}, nil
			case <-ctx.Done():
				return emailverification.RequestChange{}, ctx.Err()
			}
		})
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		require.FailNow(t, "first request did not reach its callback")
	}

	// When a second request starts and waits, then the first finishes.
	go func() {
		secondDone <- db.RequestEmailVerification(ctx, user, requestRecording(&seen, verificationLink(user, "second", 0)))
	}()
	require.Eventually(t, func() bool {
		var blocked bool
		err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%LockEmailVerificationUser%')").Scan(&blocked)
		return err == nil && blocked
	}, setupTestTimeout, 10*time.Millisecond, "second request did not wait on the user lock")
	releaseFirst()

	// Then the second request read the first's link.
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	assert.Equal(t, []time.Time{first.CreatedAt}, utcTimes(seen.LinksCreatedAt))
}

// Deleting a user deletes their links.
func TestEmailVerificationUserDeleted(t *testing.T) {
	_, conn := setupStorage(t)
	user := verificationUser(t, conn, "user@example.com")
	insertVerificationLink(t, conn, verificationLink(user, "live", time.Hour))

	_, err := conn.Exec(t.Context(), "DELETE FROM users WHERE id = $1", user)
	require.NoError(t, err)

	assert.Empty(t, savedVerificationLinks(t, conn))
}

func TestEmailVerificationMigrationDown(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	_, err := provider.DownTo(t.Context(), 14)
	require.NoError(t, err)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
}

// utcTimes returns times converted to UTC.
func utcTimes(times []time.Time) []time.Time {
	out := make([]time.Time, len(times))
	for i, at := range times {
		out[i] = at.UTC()
	}
	return out
}
