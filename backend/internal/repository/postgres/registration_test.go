package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"net/netip"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/passwordhash"
	"inbok/internal/randomsecret"
	"inbok/internal/registration"
)

// systemEmailTest is whether saved system email settings passed a test.
type systemEmailTest int

const (
	untested systemEmailTest = iota
	tested
)

// saveSystemEmail stores system email settings with the given test result.
func saveSystemEmail(t *testing.T, conn *pgx.Conn, test systemEmailTest) {
	t.Helper()

	_, err := conn.Exec(t.Context(), `INSERT INTO system_email (host, port, security, from_address, from_name, tested_at)
		VALUES ('localhost', 1025, 'none', 'inbok@localhost', 'inbok', CASE WHEN $1 THEN now() END)
		ON CONFLICT (id) DO UPDATE SET tested_at = EXCLUDED.tested_at`, test == tested)
	require.NoError(t, err)
}

func TestRegistrationStorage(t *testing.T) {
	db, conn := setupStorage(t)
	admin := systemEmailAdmin(t, conn)
	service := registration.NewService(db)

	// A new installation starts closed, without system email.
	settings, err := service.Settings(t.Context())
	require.NoError(t, err)
	assert.Equal(t, registration.Settings{Status: registration.StatusClosed}, settings)

	// Neither missing nor untested settings can open it.
	_, err = service.Save(t.Context(), admin, registration.StateOpen)
	require.ErrorIs(t, err, registration.ErrSystemEmailNotTested)
	saveSystemEmail(t, conn, untested)
	_, err = service.Save(t.Context(), admin, registration.StateOpen)
	require.ErrorIs(t, err, registration.ErrSystemEmailNotTested)

	// Tested settings can.
	saveSystemEmail(t, conn, tested)
	saved, err := service.Save(t.Context(), admin, registration.StateOpen)
	require.NoError(t, err)
	assert.Equal(t, registration.Settings{Status: registration.StatusOpen, SystemEmailTested: true}, saved)

	// Settings that lose their test pause it.
	saveSystemEmail(t, conn, untested)
	settings, err = service.Settings(t.Context())
	require.NoError(t, err)
	assert.Equal(t, registration.Settings{Status: registration.StatusPaused}, settings)

	// Closing needs no tested settings.
	saved, err = service.Save(t.Context(), admin, registration.StateClosed)
	require.NoError(t, err)
	assert.Equal(t, registration.Settings{Status: registration.StatusClosed}, saved)

	// Each save is audited with its administrator and no account; the
	// refused ones are not.
	assert.Equal(t, []auditEvent{
		{Action: "registration-opened", Actor: &admin},
		{Action: "registration-closed", Actor: &admin},
	}, auditEvents(t, conn))
}

func TestRegistrationUpdateRollsBack(t *testing.T) {
	db, conn := setupStorage(t)
	admin := systemEmailAdmin(t, conn)
	refused := errors.New("refused")

	err := db.UpdateRegistration(t.Context(), admin, func(registration.Record) (registration.State, error) {
		return "", refused
	})

	require.ErrorIs(t, err, refused)
	record, err := db.ReadRegistration(t.Context())
	require.NoError(t, err)
	assert.Equal(t, registration.Record{State: registration.StateClosed}, record)
	assert.Empty(t, auditEvents(t, conn))
}

// An open that waits for a system email save sees the save's result: new
// settings have not passed a test, so registration stays closed.
func TestRegistrationOpenWaitsForSave(t *testing.T) {
	db, conn := setupStorage(t)
	admin := systemEmailAdmin(t, conn)
	saveSystemEmail(t, conn, tested)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()

	// Given a system email save that has cleared the tested time but not
	// committed.
	save, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer save.Rollback(context.Background())
	_, err = save.Exec(ctx, "UPDATE system_email SET tested_at = NULL")
	require.NoError(t, err)

	// When an administrator opens registration meanwhile.
	opened := make(chan error, 1)
	go func() {
		_, err := registration.NewService(db).Save(ctx, admin, registration.StateOpen)
		opened <- err
	}()
	const waiting = `SELECT EXISTS (
		SELECT 1 FROM pg_stat_activity
		WHERE datname = current_database() AND wait_event_type = 'Lock'
	)`
	require.Eventually(t, func() bool {
		var blocked bool
		err := db.pool.QueryRow(ctx, waiting).Scan(&blocked)
		return err == nil && blocked
	}, setupTestTimeout, 10*time.Millisecond, "the open did not wait for the save")
	require.NoError(t, save.Commit(ctx))

	// Then the open is refused and nothing is recorded.
	require.ErrorIs(t, <-opened, registration.ErrSystemEmailNotTested)
	record, err := db.ReadRegistration(t.Context())
	require.NoError(t, err)
	assert.Equal(t, registration.Record{State: registration.StateClosed}, record)
	assert.Empty(t, auditEvents(t, conn))
}

func TestRegistrationChecks(t *testing.T) {
	conn := migrated(t)
	var pgErr *pgconn.PgError

	_, err := conn.Exec(t.Context(), "INSERT INTO registration (open) VALUES (true)")
	require.ErrorAs(t, err, &pgErr, "the table holds one row")
	assert.Equal(t, uniqueViolation, pgErr.Code)

	_, err = conn.Exec(t.Context(), "INSERT INTO registration (id) VALUES (false)")
	require.ErrorAs(t, err, &pgErr, "only the true key is allowed")
	assert.Equal(t, checkViolation, pgErr.Code)
}

var requestNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// openRegistration opens registration with tested system email.
func openRegistration(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	saveSystemEmail(t, conn, tested)
	_, err := conn.Exec(t.Context(), "UPDATE registration SET open = true")
	require.NoError(t, err)
}

// requestRegistration calls RequestRegistration for email with since one
// day before requestNow, returns what create saw, and saves change.
func requestRegistration(t *testing.T, db *DB, email string, change registration.RequestChange) registration.RequestState {
	t.Helper()

	var seen registration.RequestState
	require.NoError(t, db.RequestRegistration(t.Context(), email, requestNow.Add(-24*time.Hour), func(state registration.RequestState) (registration.RequestChange, error) {
		seen = state
		return change, nil
	}))
	return seen
}

// registrationLinkHashes lists the saved registration links' emails by hash.
func registrationLinkHashes(t *testing.T, conn *pgx.Conn) map[string]string {
	t.Helper()

	rows, err := conn.Query(t.Context(), "SELECT encode(token_hash, 'hex'), email FROM registration_links")
	require.NoError(t, err)
	links := map[string]string{}
	for rows.Next() {
		var hash, email string
		require.NoError(t, rows.Scan(&hash, &email))
		links[hash] = email
	}
	require.NoError(t, rows.Err())
	return links
}

func TestRegistrationRequestStorage(t *testing.T) {
	db, conn := setupStorage(t)
	openRegistration(t, conn)
	verificationUser(t, conn, "member@example.org")
	first := registration.Attempt{Email: "visitor@example.org", ClientPrefix: "192.0.2.1", Outcome: registration.OutcomeLink, CreatedAt: requestNow}
	link := registration.Link{TokenHash: sha256.Sum256([]byte("secret")), Email: "visitor@example.org", CreatedAt: requestNow, ExpiresAt: requestNow.Add(24 * time.Hour)}

	// A new email sees no account and no attempts, and saves its link.
	seen := requestRegistration(t, db, "visitor@example.org", registration.RequestChange{Attempt: first, Link: &link})
	assert.Equal(t, registration.RequestState{Registration: registration.Record{State: registration.StateOpen, SystemEmailTested: true}}, seen)

	// An email with an account says so, and sees the earlier attempt.
	notice := registration.Attempt{Email: "member@example.org", ClientPrefix: "2001:db8::1", Outcome: registration.OutcomeNotice, CreatedAt: requestNow}
	seen = requestRegistration(t, db, "member@example.org", registration.RequestChange{Attempt: notice})
	assert.True(t, seen.AccountExists)
	assert.Equal(t, []registration.Attempt{first}, seen.Attempts)

	seen = requestRegistration(t, db, "visitor@example.org", registration.RequestChange{Attempt: first})
	assert.False(t, seen.AccountExists)
	assert.ElementsMatch(t, []registration.Attempt{first, notice}, seen.Attempts)
	assert.Equal(t, map[string]string{hex.EncodeToString(link.TokenHash[:]): "visitor@example.org"}, registrationLinkHashes(t, conn))
}

// A request deletes attempts at or before since and leaves expired links
// to the cleanup job; a refused one deletes nothing.
func TestRegistrationRequestOldAttempts(t *testing.T) {
	for _, c := range []struct {
		name    string
		refused error
	}{
		{"accepted", nil},
		{"refused", registration.ErrThrottled},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, conn := setupStorage(t)
			user := verificationUser(t, conn, "member@example.org")
			for _, row := range []struct {
				sql  string
				args []any
			}{
				{"INSERT INTO registration_links (token_hash, email, created_at, expires_at) VALUES ('\\x01', 'a@example.org', $1, $2)", []any{requestNow.Add(-24 * time.Hour), requestNow}},
				{"INSERT INTO email_verification_links (token_hash, user_id, address, created_at, expires_at) VALUES ('\\x03', $1, 'member@example.org', $2, $2)", []any{user, requestNow}},
				{"INSERT INTO registration_attempts (email, client_prefix, outcome, created_at) VALUES ('a@example.org', '192.0.2.1', 'link', $1)", []any{requestNow.Add(-24 * time.Hour)}},
				{"INSERT INTO registration_attempts (email, client_prefix, outcome, created_at) VALUES ('a@example.org', '192.0.2.1', 'link', $1)", []any{requestNow.Add(-24*time.Hour + time.Second)}},
			} {
				_, err := conn.Exec(t.Context(), row.sql, row.args...)
				require.NoError(t, err)
			}
			attempt := registration.Attempt{Email: "b@example.org", ClientPrefix: "192.0.2.2", Outcome: registration.OutcomeSkipped, CreatedAt: requestNow}

			var seen []registration.Attempt
			err := db.RequestRegistration(t.Context(), "b@example.org", requestNow.Add(-24*time.Hour), func(state registration.RequestState) (registration.RequestChange, error) {
				seen = state.Attempts
				return registration.RequestChange{Attempt: attempt}, c.refused
			})
			require.ErrorIs(t, err, c.refused)

			assert.Len(t, seen, 1, "attempts at or before since are not read")
			var links, verifications, oldAttempts, attempts int
			require.NoError(t, conn.QueryRow(t.Context(), `SELECT
				(SELECT count(*) FROM registration_links),
				(SELECT count(*) FROM email_verification_links),
				(SELECT count(*) FROM registration_attempts WHERE created_at <= $1),
				(SELECT count(*) FROM registration_attempts)`, requestNow.Add(-24*time.Hour)).Scan(&links, &verifications, &oldAttempts, &attempts))
			want := []int{1, 1, 0, 2} // the old attempt goes; the new one is recorded
			if c.refused != nil {
				want = []int{1, 1, 1, 2} // a refused request changes nothing
			}
			assert.Equal(t, want, []int{links, verifications, oldAttempts, attempts})
		})
	}
}

// Requests take turns: the second sees the first's attempt, so the instance
// limit counts every request.
func TestRegistrationRequestSerializes(t *testing.T) {
	db, _ := setupStorage(t)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()
	ready, release := make(chan struct{}), make(chan struct{})
	releaseFirst := sync.OnceFunc(func() { close(release) })
	defer releaseFirst()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	first := registration.Attempt{Email: "a@example.org", ClientPrefix: "192.0.2.1", Outcome: registration.OutcomeLink, CreatedAt: requestNow}
	var seen []registration.Attempt

	go func() {
		firstDone <- db.RequestRegistration(ctx, "a@example.org", requestNow.Add(-24*time.Hour), func(registration.RequestState) (registration.RequestChange, error) {
			close(ready)
			select {
			case <-release:
				return registration.RequestChange{Attempt: first}, nil
			case <-ctx.Done():
				return registration.RequestChange{}, ctx.Err()
			}
		})
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		require.FailNow(t, "first request did not reach its callback")
	}
	go func() {
		secondDone <- db.RequestRegistration(ctx, "b@example.org", requestNow.Add(-24*time.Hour), func(state registration.RequestState) (registration.RequestChange, error) {
			seen = state.Attempts
			return registration.RequestChange{Attempt: first}, nil
		})
	}()
	const waiting = `SELECT EXISTS (
		SELECT 1 FROM pg_locks
		WHERE locktype = 'advisory' AND NOT granted
		  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
	)`
	require.Eventually(t, func() bool {
		var blocked bool
		err := db.pool.QueryRow(ctx, waiting).Scan(&blocked)
		return err == nil && blocked
	}, setupTestTimeout, 10*time.Millisecond, "second request did not wait for the first")
	releaseFirst()

	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	assert.Equal(t, []registration.Attempt{first}, seen)
}

func TestRegistrationLinkChecks(t *testing.T) {
	conn := migrated(t)
	for _, c := range []struct{ name, sql string }{
		{"uppercase link email", "INSERT INTO registration_links (token_hash, email, created_at, expires_at) VALUES ('\\x01', 'A@example.org', now(), now())"},
		{"uppercase attempt email", "INSERT INTO registration_attempts (email, client_prefix, outcome, created_at) VALUES ('A@example.org', '192.0.2.1', 'link', now())"},
		{"unknown outcome", "INSERT INTO registration_attempts (email, client_prefix, outcome, created_at) VALUES ('a@example.org', '192.0.2.1', 'sent', now())"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := conn.Exec(t.Context(), c.sql)

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Equal(t, checkViolation, pgErr.Code)
		})
	}
}

// The service against PostgreSQL: an email gets three messages in an hour,
// links and notices alike, and later requests for it send nothing but are
// still answered and recorded.
func TestRegistrationRequests(t *testing.T) {
	db, conn := setupStorage(t)
	openRegistration(t, conn)
	verificationUser(t, conn, "member@example.org")
	mailer := &verificationMailer{}
	requests := registration.NewRequests(db, mailer, &url.URL{Scheme: "https", Host: "mail.example.com"}, func() time.Time { return requestNow })
	client := netip.MustParseAddr("192.0.2.1")

	for range 4 {
		require.NoError(t, requests.Send(t.Context(), "visitor@example.org", client))
		require.NoError(t, requests.Send(t.Context(), "member@example.org", client))
	}

	assert.Len(t, mailer.bodies, 6)
	assert.Len(t, registrationLinkHashes(t, conn), 3)
	rows, err := conn.Query(t.Context(), "SELECT outcome FROM registration_attempts ORDER BY id")
	require.NoError(t, err)
	outcomes, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	assert.Equal(t, []string{"link", "notice", "link", "notice", "link", "notice", "skipped", "skipped"}, outcomes)
}

// Closing deletes every attempt and link, so their emails and client
// addresses are not kept while registration stays closed. Opening keeps them.
func TestRegistrationCloseDeletesRequests(t *testing.T) {
	for _, c := range []struct {
		state registration.State
		rows  int
	}{
		{registration.StateClosed, 0},
		{registration.StateOpen, 1},
	} {
		t.Run(string(c.state), func(t *testing.T) {
			db, conn := setupStorage(t)
			admin := systemEmailAdmin(t, conn)
			_, err := conn.Exec(t.Context(), `INSERT INTO registration_links (token_hash, email, created_at, expires_at) VALUES ('\x01', 'a@example.org', $1, $2)`,
				requestNow, requestNow.Add(24*time.Hour))
			require.NoError(t, err)
			_, err = conn.Exec(t.Context(), `INSERT INTO registration_attempts (email, client_prefix, outcome, created_at) VALUES ('a@example.org', '192.0.2.1', 'link', $1)`, requestNow)
			require.NoError(t, err)

			err = db.UpdateRegistration(t.Context(), admin, func(registration.Record) (registration.State, error) { return c.state, nil })
			require.NoError(t, err)

			var links, attempts int
			require.NoError(t, conn.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM registration_links), (SELECT count(*) FROM registration_attempts)").Scan(&links, &attempts))
			assert.Equal(t, []int{c.rows, c.rows}, []int{links, attempts})
		})
	}
}

// A close waits for a request in progress, then deletes what it saved, so no
// link outlives the close.
func TestRegistrationCloseWaitsForRequest(t *testing.T) {
	db, conn := setupStorage(t)
	admin := systemEmailAdmin(t, conn)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()
	ready, release := make(chan struct{}), make(chan struct{})
	releaseRequest := sync.OnceFunc(func() { close(release) })
	defer releaseRequest()
	requested, closed := make(chan error, 1), make(chan error, 1)
	link := registration.Link{TokenHash: sha256.Sum256([]byte("secret")), Email: "a@example.org", CreatedAt: requestNow, ExpiresAt: requestNow.Add(24 * time.Hour)}
	attempt := registration.Attempt{Email: "a@example.org", ClientPrefix: "192.0.2.1", Outcome: registration.OutcomeLink, CreatedAt: requestNow}

	// Given a request holding the lock, about to save its link.
	go func() {
		requested <- db.RequestRegistration(ctx, "a@example.org", requestNow.Add(-24*time.Hour), func(registration.RequestState) (registration.RequestChange, error) {
			close(ready)
			select {
			case <-release:
				return registration.RequestChange{Attempt: attempt, Link: &link}, nil
			case <-ctx.Done():
				return registration.RequestChange{}, ctx.Err()
			}
		})
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		require.FailNow(t, "the request did not reach its callback")
	}

	// When an administrator closes registration meanwhile.
	go func() {
		closed <- db.UpdateRegistration(ctx, admin, func(registration.Record) (registration.State, error) { return registration.StateClosed, nil })
	}()
	const waiting = `SELECT EXISTS (
		SELECT 1 FROM pg_locks
		WHERE locktype = 'advisory' AND NOT granted
		  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
	)`
	require.Eventually(t, func() bool {
		var blocked bool
		err := db.pool.QueryRow(ctx, waiting).Scan(&blocked)
		return err == nil && blocked
	}, setupTestTimeout, 10*time.Millisecond, "the close did not wait for the request")
	releaseRequest()

	// Then the close deletes what the request saved.
	require.NoError(t, <-requested)
	require.NoError(t, <-closed)
	assert.Empty(t, registrationLinkHashes(t, conn))
}

// saveRegistrationLink saves a live registration link to email and returns
// its secret and hash.
func saveRegistrationLink(t *testing.T, conn *pgx.Conn, email string) (string, [sha256.Size]byte) {
	t.Helper()

	secret, hash := randomsecret.New()
	_, err := conn.Exec(t.Context(), "INSERT INTO registration_links (token_hash, email, created_at, expires_at) VALUES ($1, $2, $3, $4)",
		hash[:], email, requestNow, requestNow.Add(24*time.Hour))
	require.NoError(t, err)
	return secret, hash
}

func TestRegistrationComplete(t *testing.T) {
	db, conn := setupStorage(t)
	openRegistration(t, conn)
	first, _ := saveRegistrationLink(t, conn, "visitor@example.org")
	second, _ := saveRegistrationLink(t, conn, "visitor@example.org")
	_, other := saveRegistrationLink(t, conn, "other@example.org")
	completion := registration.NewCompletion(db, func() time.Time { return requestNow })
	const password = "correct horse battery"

	user, err := completion.Complete(t.Context(), first, "visitor@example.org", password)
	require.NoError(t, err)

	// The member's email is verified: following the link proved it.
	assert.Equal(t, account.User{ID: user.ID, Email: "visitor@example.org", Role: account.RoleMember}, user)
	var role, passwordHash string
	var verifiedAt time.Time
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT role, email_verified_at, password_hash FROM users WHERE id = $1", user.ID).
		Scan(&role, &verifiedAt, &passwordHash))
	assert.Equal(t, "member", role)
	assert.Equal(t, requestNow, verifiedAt.UTC())
	ok, err := passwordhash.Verify(t.Context(), password, passwordHash)
	require.NoError(t, err)
	assert.True(t, ok, "the saved hash is of the password")

	// Every link to the email is gone; another email's stays.
	assert.Equal(t, map[string]string{hex.EncodeToString(other[:]): "other@example.org"}, registrationLinkHashes(t, conn))
	assert.Equal(t, []auditEvent{{Action: "member-registered", Target: &user.ID}}, auditEvents(t, conn))

	// So the email's other link no longer works.
	_, err = completion.Complete(t.Context(), second, "visitor@example.org", password)
	require.ErrorIs(t, err, registration.ErrLinkInvalid)
}

func TestRegistrationCompleteReads(t *testing.T) {
	db, conn := setupStorage(t)
	openRegistration(t, conn)
	verificationUser(t, conn, "member@example.org")
	_, hash := saveRegistrationLink(t, conn, "member@example.org")
	open := registration.Record{State: registration.StateOpen, SystemEmailTested: true}
	want := registration.CompleteState{
		Registration:  open,
		Link:          &registration.Link{TokenHash: hash, Email: "member@example.org", CreatedAt: requestNow, ExpiresAt: requestNow.Add(24 * time.Hour)},
		AccountExists: true,
	}
	refused := errors.New("refused")
	var locked registration.CompleteState
	lockedRead := func(hash [sha256.Size]byte, email string) registration.CompleteState {
		_, err := db.CompleteRegistration(t.Context(), hash, email, func(state registration.CompleteState) (registration.CompleteChange, error) {
			locked = state
			return registration.CompleteChange{}, refused
		})
		require.ErrorIs(t, err, refused)
		return locked
	}

	// Both reads see the link and the account.
	state, err := db.ReadCompletion(t.Context(), hash)
	require.NoError(t, err)
	assert.Equal(t, want, state)
	assert.Equal(t, want, lockedRead(hash, "member@example.org"))

	// An unknown hash has no link; the locked read still checks the email.
	unknown := sha256.Sum256([]byte("unknown"))
	state, err = db.ReadCompletion(t.Context(), unknown)
	require.NoError(t, err)
	assert.Equal(t, registration.CompleteState{Registration: open}, state)
	assert.Equal(t, registration.CompleteState{Registration: open, AccountExists: true}, lockedRead(unknown, "member@example.org"))
}

// The member gets the link's email, whatever email the caller passed, and a
// create that accepts a missing link saves nothing.
func TestRegistrationCompleteLinkEmail(t *testing.T) {
	db, conn := setupStorage(t)
	openRegistration(t, conn)
	_, hash := saveRegistrationLink(t, conn, "visitor@example.org")
	accept := func(registration.CompleteState) (registration.CompleteChange, error) {
		return registration.CompleteChange{PasswordHash: "unused", VerifiedAt: requestNow}, nil
	}

	_, err := db.CompleteRegistration(t.Context(), sha256.Sum256([]byte("unknown")), "visitor@example.org", accept)
	require.ErrorIs(t, err, errNoLink)
	user, err := db.CompleteRegistration(t.Context(), hash, "other@example.org", accept)
	require.NoError(t, err)

	assert.Equal(t, "visitor@example.org", user.Email)
	assert.Empty(t, registrationLinkHashes(t, conn))
}

func TestRegistrationCompleteRollsBack(t *testing.T) {
	db, conn := setupStorage(t)
	openRegistration(t, conn)
	_, hash := saveRegistrationLink(t, conn, "visitor@example.org")
	refused := errors.New("refused")

	_, err := db.CompleteRegistration(t.Context(), hash, "visitor@example.org", func(registration.CompleteState) (registration.CompleteChange, error) {
		return registration.CompleteChange{}, refused
	})

	require.ErrorIs(t, err, refused)
	var users int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM users").Scan(&users))
	assert.Zero(t, users)
	assert.Len(t, registrationLinkHashes(t, conn), 1)
	assert.Empty(t, auditEvents(t, conn))
}

// holdCompletion starts CompleteRegistration for hash and email, waits until
// its callback runs, and returns a function that lets the callback accept
// and waits for the result.
func holdCompletion(t *testing.T, ctx context.Context, db *DB, hash [sha256.Size]byte, email string) func() error {
	t.Helper()

	ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	go func() {
		_, err := db.CompleteRegistration(ctx, hash, email, func(registration.CompleteState) (registration.CompleteChange, error) {
			close(ready)
			select {
			case <-release:
				return registration.CompleteChange{PasswordHash: "unused", VerifiedAt: requestNow}, nil
			case <-ctx.Done():
				return registration.CompleteChange{}, ctx.Err()
			}
		})
		done <- err
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		require.FailNow(t, "the completion did not reach its callback")
	}
	return func() error {
		releaseOnce()
		return <-done
	}
}

// waitForLock fails the test unless a query waits for a lock soon.
func waitForLock(t *testing.T, ctx context.Context, db *DB, message string) {
	t.Helper()

	const waiting = `SELECT EXISTS (
		SELECT 1 FROM pg_stat_activity
		WHERE datname = current_database() AND wait_event_type = 'Lock'
	)`
	require.Eventually(t, func() bool {
		var blocked bool
		err := db.pool.QueryRow(ctx, waiting).Scan(&blocked)
		return err == nil && blocked
	}, setupTestTimeout, 10*time.Millisecond, message)
}

// A second submission waits for the first, then sees its account: the first
// deleted the link, so only the account explains the refusal.
func TestRegistrationCompleteSerializes(t *testing.T) {
	db, conn := setupStorage(t)
	openRegistration(t, conn)
	_, hash := saveRegistrationLink(t, conn, "visitor@example.org")
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()

	finish := holdCompletion(t, ctx, db, hash, "visitor@example.org")
	seen, secondDone := make(chan registration.CompleteState, 1), make(chan error, 1)
	go func() {
		_, err := db.CompleteRegistration(ctx, hash, "visitor@example.org", func(state registration.CompleteState) (registration.CompleteChange, error) {
			seen <- state
			return registration.CompleteChange{}, registration.ErrAccountExists
		})
		secondDone <- err
	}()
	waitForLock(t, ctx, db, "the second completion did not wait for the first")

	require.NoError(t, finish())
	require.ErrorIs(t, <-secondDone, registration.ErrAccountExists)
	state := <-seen
	assert.Nil(t, state.Link)
	assert.True(t, state.AccountExists)
}

// Setup waits for a completion in progress, so a setup and a completion for
// one email cannot both insert it.
func TestSetupWaitsForCompletion(t *testing.T) {
	db, conn := setupStorage(t)
	openRegistration(t, conn)
	_, hash := saveRegistrationLink(t, conn, "visitor@example.org")
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()

	finish := holdCompletion(t, ctx, db, hash, "visitor@example.org")
	setup := make(chan error, 1)
	go func() {
		_, err := db.UpdateSetup(ctx, func(account.SetupState) (account.NewAdmin, error) {
			return account.NewAdmin{Email: "admin@example.org", PasswordHash: "unused"}, nil
		})
		setup <- err
	}()
	waitForLock(t, ctx, db, "setup did not wait for the completion")

	require.NoError(t, finish())
	require.NoError(t, <-setup)
}

// A close waits for a completion in progress, so no member is created after
// registration closed.
func TestRegistrationCloseWaitsForCompletion(t *testing.T) {
	db, conn := setupStorage(t)
	admin := systemEmailAdmin(t, conn)
	openRegistration(t, conn)
	_, hash := saveRegistrationLink(t, conn, "visitor@example.org")
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()

	finish := holdCompletion(t, ctx, db, hash, "visitor@example.org")
	closed := make(chan error, 1)
	go func() {
		closed <- db.UpdateRegistration(ctx, admin, func(registration.Record) (registration.State, error) { return registration.StateClosed, nil })
	}()
	waitForLock(t, ctx, db, "the close did not wait for the completion")

	require.NoError(t, finish())
	require.NoError(t, <-closed)
	var events []string
	for _, event := range auditEvents(t, conn) {
		events = append(events, event.Action)
	}
	assert.Equal(t, []string{"member-registered", "registration-closed"}, events)
}

// Clearing the system email's tested time waits for a completion in
// progress, so none completes while registration is paused.
func TestSystemEmailSaveWaitsForCompletion(t *testing.T) {
	db, conn := setupStorage(t)
	openRegistration(t, conn)
	_, hash := saveRegistrationLink(t, conn, "visitor@example.org")
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()

	finish := holdCompletion(t, ctx, db, hash, "visitor@example.org")
	saved := make(chan error, 1)
	go func() {
		_, err := db.pool.Exec(ctx, "UPDATE system_email SET tested_at = NULL")
		saved <- err
	}()
	waitForLock(t, ctx, db, "the save did not wait for the completion")

	require.NoError(t, finish())
	require.NoError(t, <-saved)
}

// The cleanup deletes links expiring at or before the cutoff, at most limit
// at a time, and leaves a link another transaction holds for a later call.
func TestRegistrationCleanup(t *testing.T) {
	// Given links that expire at, before, and after the cutoff, one of
	// them held by another transaction.
	db, conn := setupStorage(t)
	for hash, expiresAt := range map[string]time.Time{
		"\\x01": requestNow, "\\x02": requestNow.Add(-time.Hour), "\\x03": requestNow.Add(-2 * time.Hour), "\\x04": requestNow.Add(time.Second),
	} {
		_, err := conn.Exec(t.Context(), "INSERT INTO registration_links (token_hash, email, created_at, expires_at) VALUES ($1::text::bytea, 'a@example.org', $2, $3)",
			hash, expiresAt.Add(-24*time.Hour), expiresAt)
		require.NoError(t, err)
	}
	tx, err := conn.Begin(t.Context())
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `SELECT 1 FROM registration_links WHERE token_hash = '\x03' FOR UPDATE`)
	require.NoError(t, err)

	// When the cleanup runs one link at a time, until it deletes none.
	var batches []int
	for range 3 {
		deleted, err := db.DeleteExpiredRegistrationLinks(t.Context(), requestNow, 1)
		require.NoError(t, err)
		batches = append(batches, deleted)
	}

	// Then the two expired free links go, the held and live ones stay, and
	// the held one goes once released.
	assert.Equal(t, []int{1, 1, 0}, batches)
	assert.ElementsMatch(t, []string{"03", "04"}, slices.Collect(maps.Keys(registrationLinkHashes(t, conn))))
	require.NoError(t, tx.Rollback(t.Context()))
	deleted, err := db.DeleteExpiredRegistrationLinks(t.Context(), requestNow, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
	assert.ElementsMatch(t, []string{"04"}, slices.Collect(maps.Keys(registrationLinkHashes(t, conn))))
}
