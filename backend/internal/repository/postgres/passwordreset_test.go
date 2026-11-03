package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"math/rand/v2"
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

// newResetAccounts saves a member and a bystander, each with a session, a
// sign-in challenge, a confirmed factor, a backup code, a TOTP attempt, and
// a sign-in attempt for their email.
func newResetAccounts(t *testing.T) resetAccounts {
	t.Helper()

	db, conn := setupStorage(t)
	f := resetAccounts{db: db, conn: conn}
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
		"INSERT INTO sign_in_attempts (email, client_addr, created_at) SELECT email, '192.0.2.1', now() FROM users",
	} {
		_, err := conn.Exec(t.Context(), statement)
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
	completion := passwordreset.NewCompletion(f.db, func() time.Time { return resetNow })

	// When the member follows one link.
	user, err := completion.Complete(t.Context(), first, resetPassword)
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
	_, err = completion.Complete(t.Context(), second, resetPassword)
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
		_, err := passwordreset.NewCompletion(f.db, func() time.Time { return resetNow }).Complete(ctx, secret, resetPassword)
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
	completion := passwordreset.NewCompletion(f.db, time.Now)
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
			_, resetErr = completion.Complete(t.Context(), secret, resetPassword)
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

	_, err = provider.DownTo(t.Context(), 19)
	require.NoError(t, err)

	assert.False(t, passwordResetTable(t, conn))

	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	assert.True(t, passwordResetTable(t, conn))
}

// passwordResetTable reports whether the table password_reset_links exists.
func passwordResetTable(t *testing.T, conn *pgx.Conn) bool {
	t.Helper()

	var exists bool
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT to_regclass('public.password_reset_links') IS NOT NULL").Scan(&exists))
	return exists
}
