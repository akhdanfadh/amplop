package postgres

import (
	"context"
	"crypto/sha256"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/emailverification"
	"inbok/internal/systememail"
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

// verificationRequestRecording returns a create function that records the state it reads
// and saves link, deleting links that expired by verificationNow.
func verificationRequestRecording(seen *emailverification.RequestState, link emailverification.Link) func(emailverification.RequestState) (emailverification.RequestChange, error) {
	return func(state emailverification.RequestState) (emailverification.RequestChange, error) {
		*seen = state
		return emailverification.RequestChange{Link: link, ExpiredBy: verificationNow}, nil
	}
}

// A request reads the user's state, deletes only their expired links, and
// saves the new one.
func TestEmailVerificationRequest(t *testing.T) {
	db, conn := setupStorage(t)
	user := verificationUser(t, conn, "user@example.com")
	other := verificationUser(t, conn, "other@example.com")
	verificationSystemEmail(t, conn)
	expired, live := verificationLink(user, "expired", 24*time.Hour), verificationLink(user, "live", time.Hour)
	othersExpired := verificationLink(other, "other", 48*time.Hour)
	for _, link := range []emailverification.Link{expired, live, othersExpired} {
		insertVerificationLink(t, conn, link)
	}
	added := verificationLink(user, "added", 0)
	var seen emailverification.RequestState

	err := db.RequestEmailVerification(t.Context(), user, verificationRequestRecording(&seen, added))
	require.NoError(t, err)

	assert.Equal(t, "user@example.com", seen.Email)
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

	err = db.RequestEmailVerification(t.Context(), user, verificationRequestRecording(&seen, verificationLink(user, "added", 0)))
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
		secondDone <- db.RequestEmailVerification(ctx, user, verificationRequestRecording(&seen, verificationLink(user, "second", 0)))
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

// Down removes the links table and the verified time column; Up adds them
// back.
func TestEmailVerificationMigrationDown(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())

	_, err = provider.DownTo(t.Context(), 14)
	require.NoError(t, err)

	assert.Equal(t, [2]bool{false, false}, emailVerificationSchema(t, conn))

	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	assert.Equal(t, [2]bool{true, true}, emailVerificationSchema(t, conn))
}

// emailVerificationSchema reports whether the links table and the users
// column email_verified_at exist.
func emailVerificationSchema(t *testing.T, conn *pgx.Conn) [2]bool {
	t.Helper()

	var table, column bool
	const exists = `SELECT to_regclass('public.email_verification_links') IS NOT NULL,
		EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'users' AND column_name = 'email_verified_at')`
	require.NoError(t, conn.QueryRow(t.Context(), exists).Scan(&table, &column))
	return [2]bool{table, column}
}

// utcTimes returns times converted to UTC.
func utcTimes(times []time.Time) []time.Time {
	out := make([]time.Time, len(times))
	for i, at := range times {
		out[i] = at.UTC()
	}
	return out
}

// emailVerifiedAt returns the user's email_verified_at, nil when unverified.
func emailVerifiedAt(t *testing.T, conn *pgx.Conn, userID int64) *time.Time {
	t.Helper()

	var at *time.Time
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT email_verified_at FROM users WHERE id = $1", userID).Scan(&at))
	if at != nil {
		utc := at.UTC()
		at = &utc
	}
	return at
}

// verificationRedeemRecording returns a redeem function that records the state it reads
// and answers with change and err.
func verificationRedeemRecording(seen *emailverification.RedeemState, change emailverification.RedeemChange, err error) func(emailverification.RedeemState) (emailverification.RedeemChange, error) {
	return func(state emailverification.RedeemState) (emailverification.RedeemChange, error) {
		*seen = state
		return change, err
	}
}

// Redeeming reads the link and its user, saves the verified time with an
// audit event, and deletes all the user's links; other users' links stay.
func TestEmailVerificationRedeem(t *testing.T) {
	db, conn := setupStorage(t)
	user := verificationUser(t, conn, "user@example.com")
	other := verificationUser(t, conn, "other@example.com")
	redeemed := verificationLink(user, "redeemed", time.Hour)
	othersLink := verificationLink(other, "other", time.Hour)
	for _, link := range []emailverification.Link{redeemed, verificationLink(user, "live", time.Minute), verificationLink(user, "expired", 48*time.Hour), othersLink} {
		insertVerificationLink(t, conn, link)
	}
	var seen emailverification.RedeemState

	err := db.RedeemEmailVerification(t.Context(), redeemed.TokenHash, verificationRedeemRecording(&seen, emailverification.RedeemChange{VerifiedAt: &verificationNow}, nil))
	require.NoError(t, err)

	assert.Equal(t, emailverification.RedeemState{Link: redeemed, Email: "user@example.com"}, seen)
	assert.Equal(t, &verificationNow, emailVerifiedAt(t, conn, user))
	assert.Nil(t, emailVerifiedAt(t, conn, other))
	assert.Equal(t, []emailverification.Link{othersLink}, savedVerificationLinks(t, conn))
	assert.Equal(t, []auditEvent{{Action: "email-verified", Target: &user}}, auditEvents(t, conn))
}

// A link for a verified user deletes the links and keeps the verified time,
// without an audit event.
func TestEmailVerificationRedeemAlreadyVerified(t *testing.T) {
	db, conn := setupStorage(t)
	user := verificationUser(t, conn, "user@example.com")
	earlier := verificationNow.Add(-time.Hour)
	_, err := conn.Exec(t.Context(), "UPDATE users SET email_verified_at = $1 WHERE id = $2", earlier, user)
	require.NoError(t, err)
	link := verificationLink(user, "link", time.Minute)
	insertVerificationLink(t, conn, link)
	var seen emailverification.RedeemState

	err = db.RedeemEmailVerification(t.Context(), link.TokenHash, verificationRedeemRecording(&seen, emailverification.RedeemChange{}, nil))
	require.NoError(t, err)

	assert.True(t, seen.Verified)
	assert.Equal(t, &earlier, emailVerifiedAt(t, conn, user))
	assert.Empty(t, savedVerificationLinks(t, conn))
	assert.Empty(t, auditEvents(t, conn))
}

// A refused redeem changes nothing.
func TestEmailVerificationRedeemRefused(t *testing.T) {
	db, conn := setupStorage(t)
	user := verificationUser(t, conn, "user@example.com")
	link := verificationLink(user, "link", time.Minute)
	insertVerificationLink(t, conn, link)
	var seen emailverification.RedeemState

	err := db.RedeemEmailVerification(t.Context(), link.TokenHash, verificationRedeemRecording(&seen, emailverification.RedeemChange{}, emailverification.ErrLinkInvalid))
	require.ErrorIs(t, err, emailverification.ErrLinkInvalid)

	assert.Nil(t, emailVerifiedAt(t, conn, user))
	assert.Equal(t, []emailverification.Link{link}, savedVerificationLinks(t, conn))
	assert.Empty(t, auditEvents(t, conn))
}

func TestEmailVerificationRedeemUnknown(t *testing.T) {
	db, _ := setupStorage(t)
	called := false

	err := db.RedeemEmailVerification(t.Context(), sha256.Sum256([]byte("unknown")), func(emailverification.RedeemState) (emailverification.RedeemChange, error) {
		called = true
		return emailverification.RedeemChange{}, nil
	})
	require.ErrorIs(t, err, emailverification.ErrLinkInvalid)

	assert.False(t, called)
}

// Two redeems of one link take turns; the second finds it gone.
func TestEmailVerificationRedeemSerializes(t *testing.T) {
	db, conn := setupStorage(t)
	user := verificationUser(t, conn, "user@example.com")
	link := verificationLink(user, "link", time.Minute)
	insertVerificationLink(t, conn, link)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()
	ready, release := make(chan struct{}), make(chan struct{})
	releaseFirst := sync.OnceFunc(func() { close(release) })
	defer releaseFirst()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)

	// Given a first redeem holding the user's lock.
	go func() {
		firstDone <- db.RedeemEmailVerification(ctx, link.TokenHash, func(emailverification.RedeemState) (emailverification.RedeemChange, error) {
			close(ready)
			select {
			case <-release:
				return emailverification.RedeemChange{VerifiedAt: &verificationNow}, nil
			case <-ctx.Done():
				return emailverification.RedeemChange{}, ctx.Err()
			}
		})
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		require.FailNow(t, "first redeem did not reach its callback")
	}

	// When a second redeem of the same link starts and waits, then the first
	// finishes.
	go func() {
		secondDone <- db.RedeemEmailVerification(ctx, link.TokenHash, func(emailverification.RedeemState) (emailverification.RedeemChange, error) {
			return emailverification.RedeemChange{VerifiedAt: &verificationNow}, nil
		})
	}()
	require.Eventually(t, func() bool {
		var blocked bool
		err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%LockEmailVerificationUser%')").Scan(&blocked)
		return err == nil && blocked
	}, setupTestTimeout, 10*time.Millisecond, "second redeem did not wait on the user lock")
	releaseFirst()

	// Then only the first verified, and it recorded one event.
	require.NoError(t, <-firstDone)
	require.ErrorIs(t, <-secondDone, emailverification.ErrLinkInvalid)
	assert.Equal(t, []auditEvent{{Action: "email-verified", Target: &user}}, auditEvents(t, conn))
}

// verificationMailer keeps the body of each message.
type verificationMailer struct{ bodies []string }

func (b *verificationMailer) Send(_ context.Context, outgoing systememail.Outgoing) (int64, error) {
	b.bodies = append(b.bodies, outgoing.Body)
	return 1, nil
}

// verificationSecret returns the secret in a verification email's link.
func verificationSecret(t *testing.T, body string) string {
	t.Helper()

	_, after, ok := strings.Cut(body, "/verify-email#")
	require.True(t, ok, "the body has a link")
	secret, _, _ := strings.Cut(after, "\n")
	return secret
}

// verificationService runs the service on db with its clock at *now.
func verificationService(db *DB, mailer *verificationMailer, now *time.Time) *emailverification.Service {
	return emailverification.NewService(db, mailer, &url.URL{Scheme: "https", Host: "mail.example.com"}, func() time.Time { return *now })
}

// Through the service: an older live link works, and redeeming it deletes the
// newer one.
func TestEmailVerificationServiceRedeem(t *testing.T) {
	db, conn := setupStorage(t)
	user := verificationUser(t, conn, "user@example.com")
	verificationSystemEmail(t, conn)
	mailer, now := &verificationMailer{}, verificationNow
	service := verificationService(db, mailer, &now)
	for range 2 {
		_, err := service.Request(t.Context(), user)
		require.NoError(t, err)
	}
	now = now.Add(time.Hour)

	err := service.Redeem(t.Context(), verificationSecret(t, mailer.bodies[0]))
	require.NoError(t, err)

	assert.Equal(t, &now, emailVerifiedAt(t, conn, user))
	assert.Empty(t, savedVerificationLinks(t, conn))
}

// Through the service: an expired link, or one sent to an old address, does
// not verify.
func TestEmailVerificationServiceRedeemRefused(t *testing.T) {
	for _, c := range []struct {
		name   string
		after  time.Duration
		change string
	}{
		{"expired", 24 * time.Hour, ""},
		{"address changed", time.Minute, "UPDATE users SET email = 'new@example.com'"},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, conn := setupStorage(t)
			user := verificationUser(t, conn, "user@example.com")
			verificationSystemEmail(t, conn)
			mailer, now := &verificationMailer{}, verificationNow
			service := verificationService(db, mailer, &now)
			_, err := service.Request(t.Context(), user)
			require.NoError(t, err)
			if c.change != "" {
				_, err := conn.Exec(t.Context(), c.change)
				require.NoError(t, err)
			}
			now = now.Add(c.after)

			err = service.Redeem(t.Context(), verificationSecret(t, mailer.bodies[0]))
			require.ErrorIs(t, err, emailverification.ErrLinkInvalid)

			assert.Nil(t, emailVerifiedAt(t, conn, user))
		})
	}
}
