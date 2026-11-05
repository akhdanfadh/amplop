package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"math/rand/v2"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/passwordhash"
	"inbok/internal/passwordreset"
	"inbok/internal/randomsecret"
	"inbok/internal/systememail"
)

const resetPassword = "a new reset password"

var resetNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

// resetAccounts are a member who forgot their password and a bystander.
type resetAccounts struct {
	db        *DB
	conn      *pgx.Conn
	member    int64
	bystander int64
}

// resetWorkflows saves each reset link job as a row of test_reset_link_jobs
// in the transaction it is given, as DBOS's enqueue does, so a test sees
// whether the job committed. err fails the enqueue. It implements only
// [PasswordResetWorkflows]: a call to another feature's job reaches the nil
// Workflows and panics, failing the test.
type resetWorkflows struct {
	Workflows
	err error
}

func (w resetWorkflows) EnqueuePasswordResetLink(ctx context.Context, tx pgx.Tx, attemptID, userID int64) error {
	if w.err != nil {
		return w.err
	}
	_, err := tx.Exec(ctx, "INSERT INTO test_reset_link_jobs (attempt_id, user_id) VALUES ($1, $2)", attemptID, userID)
	return err
}

// missingUserID is the ID of no user: IDs start at 1.
const missingUserID int64 = -1

// resetLinkJob is a saved reset link job: the attempt, and the account's ID,
// 0 for none.
type resetLinkJob struct {
	AttemptID int64
	UserID    int64
}

// resetLinkJobs lists the saved reset link jobs, oldest attempt first.
func resetLinkJobs(t *testing.T, conn *pgx.Conn) []resetLinkJob {
	t.Helper()
	rows, err := conn.Query(t.Context(), "SELECT attempt_id, user_id FROM test_reset_link_jobs ORDER BY attempt_id")
	require.NoError(t, err)
	jobs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[resetLinkJob])
	require.NoError(t, err)
	return jobs
}

// recordResetRequest records a request from client for email at resetNow
// and returns its attempt's ID.
func recordResetRequest(t *testing.T, ctx context.Context, f resetAccounts, client, email string) int64 {
	t.Helper()
	require.NoError(t, f.db.RecordPasswordResetRequest(ctx, client, email, resetNow.Add(-24*time.Hour), func([]time.Time) (time.Time, error) {
		return resetNow, nil
	}))
	var id int64
	require.NoError(t, f.conn.QueryRow(ctx, "SELECT max(id) FROM password_reset_attempts").Scan(&id))
	return id
}

// newResetAccounts saves a member and a bystander, each with a session, a
// sign-in challenge, a confirmed factor, a backup code, a TOTP attempt, and
// a sign-in attempt for their email.
func newResetAccounts(t *testing.T) resetAccounts {
	t.Helper()

	db, conn := setupStorage(t)
	_, err := conn.Exec(t.Context(), "CREATE TABLE test_reset_link_jobs (attempt_id bigint NOT NULL, user_id bigint NOT NULL)")
	require.NoError(t, err)
	f := resetAccounts{db: db.WithWorkflows(resetWorkflows{}), conn: conn}
	insert := func(email string) int64 {
		var id int64
		require.NoError(t, conn.QueryRow(t.Context(),
			"INSERT INTO users (email, password_hash, role, email_verified_at) VALUES ($1, 'old hash of '||$1, 'member', now()) RETURNING id",
			email).Scan(&id))
		return id
	}
	f.member = insert("member@example.com")
	f.bystander = insert("bystander@example.com")
	for _, statement := range []string{
		"INSERT INTO user_totp (user_id, key_id, secret_ciphertext, confirmed_at) SELECT id, '1', 'ciphertext', now() FROM users",
		"INSERT INTO totp_backup_codes (user_id, code_hash) SELECT id, 'backup code hash' FROM users",
		"INSERT INTO totp_attempts (user_id, created_at) SELECT id, now() FROM users",
		"INSERT INTO sessions (token_hash, user_id, expires_at) SELECT decode(md5(email), 'hex'), id, now() + interval '1 day' FROM users",
		"INSERT INTO sign_in_challenges (token_hash, user_id, expires_at) SELECT decode(md5(email), 'hex'), id, now() + interval '5 minutes' FROM users",
		"INSERT INTO sign_in_attempts (email, client_prefix, created_at) SELECT email, '192.0.2.1', now() FROM users",
	} {
		_, err = conn.Exec(t.Context(), statement)
		require.NoError(t, err)
	}
	return f
}

// saveResetLink saves a live link for userID to email and returns its secret
// and hash.
func saveResetLink(t *testing.T, conn *pgx.Conn, userID int64, email string) (string, [sha256.Size]byte) {
	t.Helper()

	secret, hash := randomsecret.New()
	_, err := conn.Exec(t.Context(), `INSERT INTO password_reset_links (token_hash, user_id, email, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, hash[:], userID, email, resetNow, resetNow.Add(time.Hour))
	require.NoError(t, err)
	return secret, hash
}

// resetMailer records the messages password reset sends.
type resetMailer struct {
	mu   sync.Mutex
	sent []systememail.Outgoing
}

// Send records outgoing and returns revision 1.
func (m *resetMailer) Send(_ context.Context, outgoing systememail.Outgoing) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, outgoing)
	return 1, nil
}

// resetPublicURL is the public URL reset links start with.
var resetPublicURL = &url.URL{Scheme: "https", Host: "mail.example.com"}

// resetBackground returns a Background, and fails the test unless its work
// finishes soon after the test ends.
func resetBackground(t *testing.T) *passwordreset.Background {
	t.Helper()
	background := passwordreset.NewBackground(discard)
	t.Cleanup(func() {
		// t.Context() has ended by now.
		ctx, cancel := context.WithTimeout(context.Background(), setupTestTimeout)
		defer cancel()
		assert.NoError(t, background.Wait(ctx), "background work still running after the test")
	})
	return background
}

// resetCompletion returns a Completion over db with the clock now, whose
// notices go nowhere.
func resetCompletion(t *testing.T, db *DB, now func() time.Time) *passwordreset.Completion {
	t.Helper()
	return passwordreset.NewCompletion(db, &resetMailer{}, resetPublicURL, resetBackground(t), now)
}

// resetSignInAttempts counts the sign-in attempts saved for email.
func resetSignInAttempts(t *testing.T, conn *pgx.Conn, email string) int {
	t.Helper()

	var count int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM sign_in_attempts WHERE email = $1", email).Scan(&count))
	return count
}

func TestPasswordResetComplete(t *testing.T) {
	// Given a member with two links and a bystander with one.
	f := newResetAccounts(t)
	first, _ := saveResetLink(t, f.conn, f.member, "member@example.com")
	second, _ := saveResetLink(t, f.conn, f.member, "member@example.com")
	saveResetLink(t, f.conn, f.bystander, "bystander@example.com")
	bystander := memberSnapshot(t, f.conn, f.bystander)
	completion := resetCompletion(t, f.db, func() time.Time { return resetNow })

	// When the member follows one link.
	user, err := completion.Complete(t.Context(), first, "member@example.com", resetPassword)
	require.NoError(t, err)

	// Then the new password is saved and the member is signed out
	// everywhere, with every link and sign-in failure gone.
	assert.Equal(t, account.User{ID: f.member, Email: "member@example.com", Role: account.RoleMember}, user)
	ok, err := passwordhash.Verify(t.Context(), resetPassword, resetPasswordHash(t, f.conn, f.member))
	require.NoError(t, err)
	assert.True(t, ok, "the saved hash is of the new password")
	for _, table := range []string{"sessions", "sign_in_challenges", "password_reset_links"} {
		assert.Zero(t, memberRowCount(t, f.conn, table, f.member), table)
	}
	assert.Zero(t, resetSignInAttempts(t, f.conn, "member@example.com"))
	// TOTP stays: the next sign-in still asks for a code.
	for _, table := range []string{"user_totp", "totp_backup_codes", "totp_attempts"} {
		assert.Equal(t, 1, memberRowCount(t, f.conn, table, f.member), table)
	}
	assert.Equal(t, []auditEvent{{Action: "password-reset", Target: &f.member}}, auditEvents(t, f.conn))
	assert.Equal(t, bystander, memberSnapshot(t, f.conn, f.bystander))
	assert.Equal(t, 1, resetSignInAttempts(t, f.conn, "bystander@example.com"))

	// And the member's other link no longer works.
	_, err = completion.Complete(t.Context(), second, "member@example.com", resetPassword)
	require.ErrorIs(t, err, passwordreset.ErrLinkInvalid)
}

// Both reads see the link, its user, and whether the user is deactivated;
// an unknown hash reads no link.
func TestPasswordResetCompleteReads(t *testing.T) {
	f := newResetAccounts(t)
	_, hash := saveResetLink(t, f.conn, f.member, "member@example.com")
	_, err := f.conn.Exec(t.Context(), "UPDATE users SET deactivated_at = now() WHERE id = $1", f.member)
	require.NoError(t, err)
	want := passwordreset.CompleteState{
		Link: &passwordreset.Link{
			TokenHash: hash, UserID: f.member, Email: "member@example.com", CreatedAt: resetNow, ExpiresAt: resetNow.Add(time.Hour),
		},
		User:        account.User{ID: f.member, Email: "member@example.com", Role: account.RoleMember},
		Deactivated: true,
	}
	refused := errors.New("refused")
	lockedRead := func(hash [sha256.Size]byte) passwordreset.CompleteState {
		var locked passwordreset.CompleteState
		_, err := f.db.CompletePasswordReset(t.Context(), hash, func(state passwordreset.CompleteState) (passwordreset.CompleteChange, error) {
			locked = state
			return passwordreset.CompleteChange{}, refused
		})
		require.ErrorIs(t, err, refused)
		return locked
	}
	unknown := sha256.Sum256([]byte("unknown"))

	state, err := f.db.ReadPasswordReset(t.Context(), hash)
	require.NoError(t, err)
	unknownState, err := f.db.ReadPasswordReset(t.Context(), unknown)
	require.NoError(t, err)
	locked, unknownLocked := lockedRead(hash), lockedRead(unknown)

	assert.Equal(t, want, state)
	assert.Equal(t, want, locked)
	assert.Equal(t, passwordreset.CompleteState{}, unknownState)
	assert.Equal(t, passwordreset.CompleteState{}, unknownLocked)
}

// A refused completion changes nothing, and one that accepts a missing link
// saves nothing.
func TestPasswordResetCompleteRollsBack(t *testing.T) {
	f := newResetAccounts(t)
	_, hash := saveResetLink(t, f.conn, f.member, "member@example.com")
	before := memberSnapshot(t, f.conn, f.member)
	refused := errors.New("refused")

	_, refusedErr := f.db.CompletePasswordReset(t.Context(), hash, func(passwordreset.CompleteState) (passwordreset.CompleteChange, error) {
		return passwordreset.CompleteChange{PasswordHash: "unused"}, refused
	})
	_, missingErr := f.db.CompletePasswordReset(t.Context(), sha256.Sum256([]byte("unknown")), func(passwordreset.CompleteState) (passwordreset.CompleteChange, error) {
		return passwordreset.CompleteChange{PasswordHash: "unused"}, nil
	})

	require.ErrorIs(t, refusedErr, refused)
	require.ErrorIs(t, missingErr, errNoLink)
	assert.Equal(t, before, memberSnapshot(t, f.conn, f.member))
	assert.Equal(t, 1, resetSignInAttempts(t, f.conn, "member@example.com"))
}

// holdInCallback runs run in a goroutine and waits until run calls the
// pause function it is given, from a storage callback that holds a lock.
// pause returns nil once finish is called, or the context's error if ctx
// ends first; the test's cleanup lets it return too. finish returns run's
// error.
func holdInCallback(t *testing.T, ctx context.Context, run func(pause func() error) error) (finish func() error) {
	t.Helper()

	ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	go func() {
		done <- run(func() error {
			close(ready)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		require.FailNow(t, "run ended before its callback", "error: %v", err)
	case <-ctx.Done():
		require.FailNow(t, "run did not reach its callback")
	}
	return func() error {
		releaseOnce()
		return <-done
	}
}

// resetPasswordHash returns the member's saved password hash.
func resetPasswordHash(t *testing.T, conn *pgx.Conn, userID int64) string {
	t.Helper()

	var hash string
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT password_hash FROM users WHERE id = $1", userID).Scan(&hash))
	return hash
}

// eventActions returns the actions of events.
func eventActions(events []auditEvent) []string {
	var actions []string
	for _, event := range events {
		actions = append(actions, event.Action)
	}
	return actions
}

// A second completion of one link waits for the first, then finds the link
// gone.
func TestPasswordResetCompleteSerializes(t *testing.T) {
	f := newResetAccounts(t)
	_, hash := saveResetLink(t, f.conn, f.member, "member@example.com")
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()

	// Given a completion that holds the member's lock in its callback.
	finish := holdInCallback(t, ctx, func(pause func() error) error {
		_, err := f.db.CompletePasswordReset(ctx, hash, func(passwordreset.CompleteState) (passwordreset.CompleteChange, error) {
			return passwordreset.CompleteChange{PasswordHash: "first"}, pause()
		})
		return err
	})

	// When a second completion of the link starts, and the first then
	// commits.
	seen, secondDone := make(chan passwordreset.CompleteState, 1), make(chan error, 1)
	go func() {
		_, err := f.db.CompletePasswordReset(ctx, hash, func(state passwordreset.CompleteState) (passwordreset.CompleteChange, error) {
			seen <- state
			return passwordreset.CompleteChange{}, passwordreset.ErrLinkInvalid
		})
		secondDone <- err
	}()
	waitForLock(t, ctx, f.db, "the second completion did not wait for the first")
	firstErr := finish()

	// Then the second sees no link.
	require.NoError(t, firstErr)
	require.ErrorIs(t, <-secondDone, passwordreset.ErrLinkInvalid)
	assert.Nil(t, (<-seen).Link)
	assert.Equal(t, "first", resetPasswordHash(t, f.conn, f.member))
}

// A completion that reaches the member's lock while a deactivation holds it
// sees the deactivation and changes nothing.
func TestPasswordResetWaitsForDeactivation(t *testing.T) {
	f := newDeactivatedAccounts(t, false)
	secret, _ := saveResetLink(t, f.conn, f.member, "member@example.com")
	before := resetPasswordHash(t, f.conn, f.member)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()

	// Given a deactivation that holds the member's lock.
	finish := holdInCallback(t, ctx, func(pause func() error) error {
		_, err := f.db.UpdateMember(ctx, f.admin, f.member, func(account.Member) (account.MemberChange, error) {
			return account.MemberChange{Action: account.MemberDeactivate, At: time.Now()}, pause()
		})
		return err
	})

	// When a completion waits for the lock, and the deactivation commits.
	resetDone := make(chan error, 1)
	go func() {
		_, err := resetCompletion(t, f.db, func() time.Time { return resetNow }).Complete(ctx, secret, "member@example.com", resetPassword)
		resetDone <- err
	}()
	waitForLock(t, ctx, f.db, "the completion did not wait for the deactivation")
	deactivateErr := finish()

	// Then the completion is refused and the old password stays.
	require.NoError(t, deactivateErr)
	require.ErrorIs(t, <-resetDone, passwordreset.ErrLinkInvalid)
	assert.Equal(t, before, resetPasswordHash(t, f.conn, f.member))
	assert.Equal(t, []string{"member-deactivated"}, eventActions(auditEvents(t, f.conn)))
	assert.Zero(t, memberRowCount(t, f.conn, "password_reset_links", f.member))
}

// A deactivation that reaches the member's lock while a completion holds it
// follows the reset, and still ends the member's sessions.
func TestDeactivationWaitsForPasswordReset(t *testing.T) {
	f := newDeactivatedAccounts(t, false)
	_, hash := saveResetLink(t, f.conn, f.member, "member@example.com")
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()

	// Given a completion that holds the member's lock.
	finish := holdInCallback(t, ctx, func(pause func() error) error {
		_, err := f.db.CompletePasswordReset(ctx, hash, func(passwordreset.CompleteState) (passwordreset.CompleteChange, error) {
			return passwordreset.CompleteChange{PasswordHash: "reset"}, pause()
		})
		return err
	})

	// When a deactivation waits for the lock, and the completion commits.
	deactivateDone := make(chan error, 1)
	go func() {
		_, err := f.members.Deactivate(ctx, f.admin, f.member)
		deactivateDone <- err
	}()
	waitForLock(t, ctx, f.db, "the deactivation did not wait for the completion")
	resetErr := finish()

	// Then both commit, the reset first.
	require.NoError(t, resetErr)
	require.NoError(t, <-deactivateDone)
	assert.Equal(t, "reset", resetPasswordHash(t, f.conn, f.member))
	assert.Equal(t, []string{"password-reset", "member-deactivated"}, eventActions(auditEvents(t, f.conn)))
	assert.Zero(t, memberRowCount(t, f.conn, "password_reset_links", f.member))
}

// A completion racing a deactivation never resets a deactivated account:
// either the reset commits first and the deactivation follows it, or the
// completion sees the deactivation and changes nothing. No link survives.
// The two tests above force each order; this one lets the timing vary.
func TestPasswordResetDeactivationRace(t *testing.T) {
	const rounds = 15
	f := newDeactivatedAccounts(t, false)
	completion := resetCompletion(t, f.db, time.Now)
	outcomes := map[bool]int{} // whether the completion was refused
	// A completion spends most of its time hashing the password. Every
	// other round deactivates at once, which commits before the completion
	// reads the link; the rest wait a random time of up to twice the hash,
	// landing before, during, or after it.
	started := time.Now()
	hashPassword(t, resetPassword)
	window := 2 * time.Since(started)

	for round := range rounds {
		_, err := f.members.Reactivate(t.Context(), f.admin, f.member)
		require.NoError(t, err)
		secret, hash := randomsecret.New()
		_, err = f.conn.Exec(t.Context(), `INSERT INTO password_reset_links (token_hash, user_id, email, created_at, expires_at)
			VALUES ($1, $2, 'member@example.com', now(), now() + interval '1 hour')`, hash[:], f.member)
		require.NoError(t, err)
		var delay time.Duration
		if round%2 == 1 {
			delay = rand.N(window)
		}
		before := len(auditEvents(t, f.conn))

		var wg sync.WaitGroup
		var resetErr, deactivateErr error
		wg.Go(func() {
			_, resetErr = completion.Complete(t.Context(), secret, "member@example.com", resetPassword)
		})
		wg.Go(func() {
			time.Sleep(delay)
			_, deactivateErr = f.members.Deactivate(t.Context(), f.admin, f.member)
		})
		wg.Wait()

		require.NoError(t, deactivateErr)
		actions := eventActions(auditEvents(t, f.conn)[before:])
		if resetErr != nil {
			require.ErrorIs(t, resetErr, passwordreset.ErrLinkInvalid)
			assert.Equal(t, []string{"member-deactivated"}, actions, "a refused completion records nothing")
		} else {
			assert.Equal(t, []string{"password-reset", "member-deactivated"}, actions, "the deactivation follows the reset")
		}
		outcomes[resetErr != nil]++
		assert.Zero(t, memberRowCount(t, f.conn, "password_reset_links", f.member))
		assert.Zero(t, memberRowCount(t, f.conn, "sessions", f.member))
	}
	t.Logf("completions refused: %d, reset then deactivated: %d", outcomes[true], outcomes[false])
	assert.Positive(t, outcomes[true], "some completion must see the deactivation")
}

func TestPasswordResetMigrationDown(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())

	_, err = provider.DownTo(t.Context(), 20)
	require.NoError(t, err)
	attemptsDown := passwordResetTables(t, conn)
	_, err = provider.DownTo(t.Context(), 19)
	require.NoError(t, err)
	linksDown := passwordResetTables(t, conn)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	assert.Equal(t, [2]bool{true, false}, attemptsDown)
	assert.Equal(t, [2]bool{false, false}, linksDown)
	assert.Equal(t, [2]bool{true, true}, passwordResetTables(t, conn))
}

// passwordResetTables reports whether the tables password_reset_links and
// password_reset_attempts exist.
func passwordResetTables(t *testing.T, conn *pgx.Conn) [2]bool {
	t.Helper()

	var links, attempts bool
	require.NoError(t, conn.QueryRow(t.Context(), `SELECT to_regclass('public.password_reset_links') IS NOT NULL,
		to_regclass('public.password_reset_attempts') IS NOT NULL`).Scan(&links, &attempts))
	return [2]bool{links, attempts}
}

// resetAttempt is one saved password reset attempt.
type resetAttempt struct {
	Client    string
	UserID    *int64
	LinkedAt  *time.Time
	CreatedAt time.Time
}

// resetAttempts lists every saved attempt, oldest first.
func resetAttempts(t *testing.T, conn *pgx.Conn) []resetAttempt {
	t.Helper()

	rows, err := conn.Query(t.Context(), "SELECT coalesce(client_prefix, ''), user_id, linked_at, created_at FROM password_reset_attempts ORDER BY id")
	require.NoError(t, err)
	attempts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (resetAttempt, error) {
		var attempt resetAttempt
		err := row.Scan(&attempt.Client, &attempt.UserID, &attempt.LinkedAt, &attempt.CreatedAt)
		attempt.CreatedAt = attempt.CreatedAt.UTC()
		if attempt.LinkedAt != nil {
			linkedAt := attempt.LinkedAt.UTC()
			attempt.LinkedAt = &linkedAt
		}
		return attempt, err
	})
	require.NoError(t, err)
	return attempts
}

// insertResetAttempt saves an attempt from client, made age before
// resetNow, that saved no link.
func insertResetAttempt(t *testing.T, conn *pgx.Conn, client string, age time.Duration) {
	t.Helper()

	_, err := conn.Exec(t.Context(), "INSERT INTO password_reset_attempts (client_prefix, created_at) VALUES ($1, $2)",
		client, resetNow.Add(-age))
	require.NoError(t, err)
}

// insertLinkedResetAttempt saves an attempt from client, made age before
// resetNow, whose link for userID was saved linkedAge before resetNow.
func insertLinkedResetAttempt(t *testing.T, conn *pgx.Conn, client string, userID int64, age, linkedAge time.Duration) {
	t.Helper()

	_, err := conn.Exec(t.Context(), "INSERT INTO password_reset_attempts (client_prefix, user_id, linked_at, created_at) VALUES ($1, $2, $3, $4)",
		client, userID, resetNow.Add(-linkedAge), resetNow.Add(-age))
	require.NoError(t, err)
}

// at returns resetNow less age, as a pointer.
func at(age time.Duration) *time.Time {
	t := resetNow.Add(-age)
	return &t
}

// A request sees only its client's attempts within the longest window,
// leaves older ones for the hourly cleanup, records one at the time check
// returns, and saves its job for the account with the email.
func TestPasswordResetRequestRecords(t *testing.T) {
	f := newResetAccounts(t)
	insertResetAttempt(t, f.conn, "192.0.2.1/32", 25*time.Hour)
	insertResetAttempt(t, f.conn, "192.0.2.1/32", time.Minute)
	// Its request is old, but its link still counts for the account.
	insertLinkedResetAttempt(t, f.conn, "198.51.100.9/32", f.member, 25*time.Hour, 23*time.Hour)
	var seen []time.Time

	err := f.db.RecordPasswordResetRequest(t.Context(), "192.0.2.1/32", "member@example.com", resetNow.Add(-24*time.Hour), func(times []time.Time) (time.Time, error) {
		seen = times
		return resetNow, nil
	})
	require.NoError(t, err)

	assert.Equal(t, []time.Time{resetNow.Add(-time.Minute)}, seen)
	assert.Equal(t, []resetAttempt{
		{Client: "192.0.2.1/32", CreatedAt: resetNow.Add(-25 * time.Hour)},
		{Client: "192.0.2.1/32", CreatedAt: resetNow.Add(-time.Minute)},
		{Client: "198.51.100.9/32", UserID: &f.member, LinkedAt: at(23 * time.Hour), CreatedAt: resetNow.Add(-25 * time.Hour)},
		{Client: "192.0.2.1/32", CreatedAt: resetNow},
	}, resetAttempts(t, f.conn))
	var savedID int64
	require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT max(id) FROM password_reset_attempts").Scan(&savedID))
	assert.Equal(t, []resetLinkJob{{AttemptID: savedID, UserID: f.member}}, resetLinkJobs(t, f.conn))
}

// An email no account has saves the same attempt and job, with no account.
func TestPasswordResetRequestUnknownEmail(t *testing.T) {
	f := newResetAccounts(t)

	id := recordResetRequest(t, t.Context(), f, "192.0.2.1/32", "nobody@example.com")

	assert.Equal(t, []resetAttempt{{Client: "192.0.2.1/32", CreatedAt: resetNow}}, resetAttempts(t, f.conn))
	assert.Equal(t, []resetLinkJob{{AttemptID: id}}, resetLinkJobs(t, f.conn))
}

// A job that cannot be saved rolls the attempt back with it, and so does a
// DB without workflows.
func TestPasswordResetRequestJobRollsBack(t *testing.T) {
	f := newResetAccounts(t)
	failed := errors.New("queue unavailable")
	accept := func([]time.Time) (time.Time, error) { return resetNow, nil }

	failedErr := f.db.WithWorkflows(resetWorkflows{err: failed}).RecordPasswordResetRequest(t.Context(), "192.0.2.1/32", "member@example.com", resetNow, accept)
	plainErr := (&DB{pool: f.db.pool}).RecordPasswordResetRequest(t.Context(), "192.0.2.1/32", "member@example.com", resetNow, accept)

	require.ErrorIs(t, failedErr, failed)
	require.ErrorIs(t, plainErr, errNoWorkflows)
	assert.Empty(t, resetAttempts(t, f.conn))
	assert.Empty(t, resetLinkJobs(t, f.conn))
}

// A refused request records nothing.
func TestPasswordResetRequestRefused(t *testing.T) {
	f := newResetAccounts(t)
	refused := errors.New("refused")

	err := f.db.RecordPasswordResetRequest(t.Context(), "192.0.2.1/32", "member@example.com", resetNow.Add(-24*time.Hour), func([]time.Time) (time.Time, error) {
		return time.Time{}, refused
	})

	require.ErrorIs(t, err, refused)
	assert.Empty(t, resetAttempts(t, f.conn))
	assert.Empty(t, resetLinkJobs(t, f.conn))
}

// One client's requests wait for each other, so its limit counts every
// one; another client's do not wait.
func TestPasswordResetRequestSerializes(t *testing.T) {
	f := newResetAccounts(t)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()
	since := resetNow.Add(-24 * time.Hour)
	record := func(client string, seen chan<- []time.Time) error {
		return f.db.RecordPasswordResetRequest(ctx, client, "member@example.com", since, func(times []time.Time) (time.Time, error) {
			seen <- times
			return resetNow, nil
		})
	}

	// Given a request from one client that holds its lock.
	finish := holdInCallback(t, ctx, func(pause func() error) error {
		return f.db.RecordPasswordResetRequest(ctx, "192.0.2.1/32", "member@example.com", since, func([]time.Time) (time.Time, error) {
			return resetNow, pause()
		})
	})

	// When another client asks, and then the same client asks again.
	otherSeen := make(chan []time.Time, 1)
	require.NoError(t, record("198.51.100.9/32", otherSeen))
	sameSeen, sameDone := make(chan []time.Time, 1), make(chan error, 1)
	go func() { sameDone <- record("192.0.2.1/32", sameSeen) }()
	waitForLock(t, ctx, f.db, "the same client's request did not wait")
	firstErr := finish()

	// Then the other client did not wait, and the same client's second
	// request counted the first.
	require.NoError(t, firstErr)
	require.NoError(t, <-sameDone)
	assert.Empty(t, <-otherSeen)
	assert.Equal(t, []time.Time{resetNow}, <-sameSeen)
}

// The link step reads the account with the ID, whether it may reset, and
// when links were saved for it within the window; a missing account reads
// as none.
func TestPasswordResetLinkState(t *testing.T) {
	f := newResetAccounts(t)
	insertLinkedResetAttempt(t, f.conn, "192.0.2.1/32", f.member, 25*time.Hour, 25*time.Hour)
	// The account limits count when the link was saved, not the request.
	insertLinkedResetAttempt(t, f.conn, "192.0.2.1/32", f.member, 25*time.Hour, time.Hour)
	insertLinkedResetAttempt(t, f.conn, "192.0.2.1/32", f.bystander, time.Minute, time.Minute)
	_, err := f.conn.Exec(t.Context(), "UPDATE users SET email_verified_at = NULL, deactivated_at = now() WHERE id = $1", f.member)
	require.NoError(t, err)
	read := func(userID int64) passwordreset.LinkState {
		var state passwordreset.LinkState
		require.NoError(t, f.db.CreatePasswordResetLink(t.Context(), 1, userID, resetNow.Add(-24*time.Hour), func(s passwordreset.LinkState) (passwordreset.LinkChange, error) {
			state = s
			return passwordreset.LinkChange{}, nil
		}))
		return state
	}

	member, unknown := read(f.member), read(missingUserID)

	assert.Equal(t, passwordreset.LinkState{
		Account: &passwordreset.Account{
			User:        account.User{ID: f.member, Email: "member@example.com", Role: account.RoleMember},
			Deactivated: true,
		},
		LinksSent: []time.Time{resetNow.Add(-time.Hour)},
	}, member)
	assert.Equal(t, passwordreset.LinkState{}, unknown)
}

// A link is saved for the locked account and marks its attempt; other
// links stay, expired ones included, for the hourly cleanup.
func TestPasswordResetLinkSaves(t *testing.T) {
	f := newResetAccounts(t)
	_, live := saveResetLink(t, f.conn, f.bystander, "bystander@example.com")
	_, err := f.conn.Exec(t.Context(), `INSERT INTO password_reset_links (token_hash, user_id, email, created_at, expires_at)
		VALUES ('expired', $1, 'bystander@example.com', $2, $3)`, f.bystander, resetNow.Add(-2*time.Hour), resetNow.Add(-time.Hour))
	require.NoError(t, err)
	id := recordResetRequest(t, t.Context(), f, "192.0.2.1/32", "member@example.com")
	hash := sha256.Sum256([]byte("new link"))

	err = f.db.CreatePasswordResetLink(t.Context(), id, f.member, resetNow.Add(-24*time.Hour), func(passwordreset.LinkState) (passwordreset.LinkChange, error) {
		return passwordreset.LinkChange{
			Link: &passwordreset.Link{TokenHash: hash, Email: "other@example.com", CreatedAt: resetNow, ExpiresAt: resetNow.Add(time.Hour)},
		}, nil
	})
	require.NoError(t, err)

	state, err := f.db.ReadPasswordReset(t.Context(), hash)
	require.NoError(t, err)
	require.NotNil(t, state.Link)
	assert.Equal(t, passwordreset.Link{
		TokenHash: hash, UserID: f.member, Email: "member@example.com", CreatedAt: resetNow, ExpiresAt: resetNow.Add(time.Hour),
	}, *state.Link, "the account decides the user and the email")
	assert.Equal(t, []resetAttempt{{Client: "192.0.2.1/32", UserID: &f.member, LinkedAt: at(0), CreatedAt: resetNow}}, resetAttempts(t, f.conn))
	assert.Equal(t, 2, memberRowCount(t, f.conn, "password_reset_links", f.bystander), "the request deletes no other link")
	liveState, err := f.db.ReadPasswordReset(t.Context(), live)
	require.NoError(t, err)
	assert.NotNil(t, liveState.Link)
}

// saveLinkFor saves a link named seed for userID for the attempt with
// attemptID at resetNow.
func saveLinkFor(t *testing.T, f resetAccounts, attemptID, userID int64, seed string) {
	t.Helper()
	require.NoError(t, f.db.CreatePasswordResetLink(t.Context(), attemptID, userID, resetNow.Add(-24*time.Hour), func(passwordreset.LinkState) (passwordreset.LinkChange, error) {
		return passwordreset.LinkChange{
			Link: &passwordreset.Link{TokenHash: sha256.Sum256([]byte(seed)), CreatedAt: resetNow, ExpiresAt: resetNow.Add(time.Hour)},
		}, nil
	}))
}

// Every saved link counts toward the account's limits: a retry's link, and
// one whose attempt the cleanup deleted, each get a row of their own, with
// no client, so the client's limit does not count them.
func TestPasswordResetLinkCountsEach(t *testing.T) {
	f := newResetAccounts(t)
	id := recordResetRequest(t, t.Context(), f, "192.0.2.1/32", "member@example.com")

	saveLinkFor(t, f, id, f.member, "first try")
	saveLinkFor(t, f, id, f.member, "retry")
	saveLinkFor(t, f, id+1, f.member, "attempt deleted")

	assert.Equal(t, []resetAttempt{
		{Client: "192.0.2.1/32", UserID: &f.member, LinkedAt: at(0), CreatedAt: resetNow},
		{UserID: &f.member, LinkedAt: at(0), CreatedAt: resetNow},
		{UserID: &f.member, LinkedAt: at(0), CreatedAt: resetNow},
	}, resetAttempts(t, f.conn))
	var seen passwordreset.LinkState
	require.NoError(t, f.db.CreatePasswordResetLink(t.Context(), id, f.member, resetNow.Add(-24*time.Hour), func(state passwordreset.LinkState) (passwordreset.LinkChange, error) {
		seen = state
		return passwordreset.LinkChange{}, nil
	}))
	assert.Len(t, seen.LinksSent, 3)
	var clientTimes []time.Time
	require.NoError(t, f.db.RecordPasswordResetRequest(t.Context(), "192.0.2.1/32", "member@example.com", resetNow.Add(-24*time.Hour), func(times []time.Time) (time.Time, error) {
		clientTimes = times
		return resetNow, nil
	}))
	assert.Len(t, clientTimes, 1, "the client's limit counts its request, not the extra links")
}

// A link for a missing account is refused, and a refused create saves
// nothing.
func TestPasswordResetLinkRollsBack(t *testing.T) {
	f := newResetAccounts(t)
	refused := errors.New("refused")
	link := &passwordreset.Link{TokenHash: sha256.Sum256([]byte("link")), CreatedAt: resetNow, ExpiresAt: resetNow.Add(time.Hour)}

	unknownErr := f.db.CreatePasswordResetLink(t.Context(), 1, missingUserID, resetNow, func(passwordreset.LinkState) (passwordreset.LinkChange, error) {
		return passwordreset.LinkChange{Link: link}, nil
	})
	refusedErr := f.db.CreatePasswordResetLink(t.Context(), 1, f.member, resetNow, func(passwordreset.LinkState) (passwordreset.LinkChange, error) {
		return passwordreset.LinkChange{Link: link}, refused
	})

	require.ErrorIs(t, unknownErr, errNoAccount)
	require.ErrorIs(t, refusedErr, refused)
	var links int
	require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM password_reset_links").Scan(&links))
	assert.Zero(t, links)
}

// One account's link steps wait for each other, so the email limits count
// every link.
func TestPasswordResetLinkSerializes(t *testing.T) {
	f := newResetAccounts(t)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()
	since := resetNow.Add(-24 * time.Hour)
	ids := make([]int64, 2)
	for i := range ids {
		ids[i] = recordResetRequest(t, ctx, f, "192.0.2.1/32", "member@example.com")
	}

	// Given a link step that holds the member's lock and sends a link.
	finish := holdInCallback(t, ctx, func(pause func() error) error {
		return f.db.CreatePasswordResetLink(ctx, ids[0], f.member, since, func(passwordreset.LinkState) (passwordreset.LinkChange, error) {
			return passwordreset.LinkChange{
				Link: &passwordreset.Link{TokenHash: sha256.Sum256([]byte("first")), CreatedAt: resetNow, ExpiresAt: resetNow.Add(time.Hour)},
			}, pause()
		})
	})

	// When a second one for the member starts, and the first commits.
	seen, secondDone := make(chan passwordreset.LinkState, 1), make(chan error, 1)
	go func() {
		secondDone <- f.db.CreatePasswordResetLink(ctx, ids[1], f.member, since, func(state passwordreset.LinkState) (passwordreset.LinkChange, error) {
			seen <- state
			return passwordreset.LinkChange{}, nil
		})
	}()
	waitForLock(t, ctx, f.db, "the second link step did not wait for the first")
	firstErr := finish()

	// Then the second sees the first's link.
	require.NoError(t, firstErr)
	require.NoError(t, <-secondDone)
	assert.Equal(t, []time.Time{resetNow}, (<-seen).LinksSent)
}

// A link step that waits for a deactivation holding the account's lock sees
// the deactivation, so no link is sent to the account it closed.
func TestPasswordResetLinkWaitsForDeactivation(t *testing.T) {
	f := newDeactivatedAccounts(t, false)
	_, err := f.conn.Exec(t.Context(), "UPDATE users SET email_verified_at = now() WHERE id = $1", f.member)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()

	// Given a deactivation that holds the member's lock.
	finish := holdInCallback(t, ctx, func(pause func() error) error {
		_, err := f.db.UpdateMember(ctx, f.admin, f.member, func(account.Member) (account.MemberChange, error) {
			return account.MemberChange{Action: account.MemberDeactivate, At: time.Now()}, pause()
		})
		return err
	})

	// When a link step for the member waits for the lock, and the
	// deactivation commits.
	seen, linkDone := make(chan passwordreset.LinkState, 1), make(chan error, 1)
	go func() {
		linkDone <- f.db.CreatePasswordResetLink(ctx, 1, f.member, resetNow.Add(-24*time.Hour), func(state passwordreset.LinkState) (passwordreset.LinkChange, error) {
			seen <- state
			return passwordreset.LinkChange{}, nil
		})
	}()
	waitForLock(t, ctx, f.db, "the link step did not wait for the deactivation")
	deactivateErr := finish()

	// Then the link step reads the account as deactivated.
	require.NoError(t, deactivateErr)
	require.NoError(t, <-linkDone)
	state := <-seen
	require.NotNil(t, state.Account)
	assert.True(t, state.Account.Deactivated)
}

// A request saves its job, which mails a link; following it resets the
// password, and the owner is told.
func TestPasswordResetFlow(t *testing.T) {
	f := newResetAccounts(t)
	mailer, background := &resetMailer{}, resetBackground(t)
	clock := func() time.Time { return resetNow }
	requests := passwordreset.NewRequests(f.db, clock)
	links := passwordreset.NewLinks(f.db, mailer, resetPublicURL, clock)
	completion := passwordreset.NewCompletion(f.db, mailer, resetPublicURL, background, clock)

	err := requests.Request(t.Context(), "Member@Example.com", netip.MustParseAddr("192.0.2.1"))
	require.NoError(t, err)
	jobs := resetLinkJobs(t, f.conn)
	require.Len(t, jobs, 1)
	outcome, err := links.Send(t.Context(), jobs[0].AttemptID, jobs[0].UserID)
	require.NoError(t, err)
	assert.Equal(t, passwordreset.LinkSent, outcome)
	require.Len(t, mailer.sent, 1)
	_, secret, ok := strings.Cut(mailer.sent[0].Body, "https://mail.example.com/reset-password#")
	require.True(t, ok)
	secret, _, _ = strings.Cut(secret, "\n")
	user, err := completion.Complete(t.Context(), secret, "member@example.com", resetPassword)
	require.NoError(t, err)
	require.NoError(t, background.Wait(t.Context()))

	assert.Equal(t, f.member, user.ID)
	ok, err = passwordhash.Verify(t.Context(), resetPassword, resetPasswordHash(t, f.conn, f.member))
	require.NoError(t, err)
	assert.True(t, ok)
	require.Len(t, mailer.sent, 2)
	assert.Equal(t, "password changed notice", mailer.sent[1].Purpose)
	assert.Equal(t, "member@example.com", mailer.sent[1].To)
}

// The cleanup deletes links expiring at or before the cutoff, at most limit
// at a time, and leaves a link another transaction holds for a later call.
func TestPasswordResetCleanupLinks(t *testing.T) {
	// Given links that expire at, before, and after the cutoff, one of
	// them held by another transaction.
	f := newResetAccounts(t)
	for hash, expiresAt := range map[string]time.Time{
		"\\x01": resetNow, "\\x02": resetNow.Add(-time.Hour), "\\x03": resetNow.Add(-2 * time.Hour), "\\x04": resetNow.Add(time.Second),
	} {
		_, err := f.conn.Exec(t.Context(), `INSERT INTO password_reset_links (token_hash, user_id, email, created_at, expires_at)
			VALUES ($1::text::bytea, $2, 'member@example.com', $3, $4)`, hash, f.member, expiresAt.Add(-time.Hour), expiresAt)
		require.NoError(t, err)
	}
	tx, err := f.conn.Begin(t.Context())
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `SELECT 1 FROM password_reset_links WHERE token_hash = '\x03' FOR UPDATE`)
	require.NoError(t, err)

	// When the cleanup runs one link at a time, until it deletes none.
	var batches []int
	for range 3 {
		deleted, err := f.db.DeleteExpiredPasswordResetLinks(t.Context(), resetNow, 1)
		require.NoError(t, err)
		batches = append(batches, deleted)
	}

	// Then the two expired free links go, the held and live ones stay, and
	// the held one goes once released.
	assert.Equal(t, []int{1, 1, 0}, batches)
	assert.Equal(t, []string{"03", "04"}, resetLinkHashes(t, f.conn))
	require.NoError(t, tx.Rollback(t.Context()))
	deleted, err := f.db.DeleteExpiredPasswordResetLinks(t.Context(), resetNow, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
	assert.Equal(t, []string{"04"}, resetLinkHashes(t, f.conn))
}

// resetLinkHashes lists the saved links' hashes in hex, in order.
func resetLinkHashes(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()

	rows, err := conn.Query(t.Context(), "SELECT encode(token_hash, 'hex') FROM password_reset_links ORDER BY 1")
	require.NoError(t, err)
	hashes, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return hashes
}

// The cleanup deletes attempts whose times are all at or before the cutoff,
// at most limit at a time.
func TestPasswordResetCleanupAttempts(t *testing.T) {
	// Given attempts on both sides of a 24-hour cutoff, one of them old but
	// linked recently.
	f := newResetAccounts(t)
	insertResetAttempt(t, f.conn, "old", 25*time.Hour)
	insertResetAttempt(t, f.conn, "at cutoff", 24*time.Hour)
	insertResetAttempt(t, f.conn, "recent", time.Hour)
	insertLinkedResetAttempt(t, f.conn, "old, linked recently", f.member, 26*time.Hour, time.Hour)
	insertLinkedResetAttempt(t, f.conn, "old, linked long ago", f.member, 26*time.Hour, 25*time.Hour)
	insertLinkedResetAttempt(t, f.conn, "old, linked at cutoff", f.member, 26*time.Hour, 24*time.Hour)
	since := resetNow.Add(-24 * time.Hour)

	// When the cleanup runs two attempts at a time, until it deletes none.
	var batches []int
	for range 3 {
		deleted, err := f.db.DeleteOldPasswordResetAttempts(t.Context(), since, 2)
		require.NoError(t, err)
		batches = append(batches, deleted)
	}

	// Then only attempts that no limit counts are gone.
	assert.Equal(t, []int{2, 2, 0}, batches)
	var left []string
	for _, attempt := range resetAttempts(t, f.conn) {
		left = append(left, attempt.Client)
	}
	assert.Equal(t, []string{"recent", "old, linked recently"}, left)
}
