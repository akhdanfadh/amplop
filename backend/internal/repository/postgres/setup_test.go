package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
)

const (
	setupTestHash    = "$argon2id$v=19$m=64,t=1,p=1$c29tZXNhbHQ$ZVrRXqxlLcWfcXCnMyv0m4Rpvh/bnCi7"
	setupTestToken   = "test-only-setup-token-for-postgres-tests"
	setupTestTimeout = 5 * time.Second
)

type setupResult struct {
	user account.User
	err  error
}

func setupStorage(t *testing.T) (*DB, *pgx.Conn) {
	t.Helper()

	conn := migrated(t)
	db, err := Open(t.Context(), conn.Config().ConnString())
	require.NoError(t, err)
	t.Cleanup(db.Close)
	return db, conn
}

func countUsers(t *testing.T, conn *pgx.Conn) int64 {
	t.Helper()

	var count int64
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM users").Scan(&count))
	return count
}

func TestUsersChecks(t *testing.T) {
	conn := migrated(t)
	const insert = "INSERT INTO users (email, password_hash, role) VALUES ($1, 'test hash', $2)"
	longest := strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)

	_, err := conn.Exec(t.Context(), insert, longest, "admin")
	require.NoError(t, err, "254-character email")
	_, err = conn.Exec(t.Context(), insert, "member@example.com", "member")
	require.NoError(t, err)

	cases := []struct {
		name, email, role string
	}{
		{"empty email", "", "admin"},
		{"short email", "a@", "admin"},
		{"long email", "a" + longest, "admin"},
		{"uppercase ASCII in email", "Other@example.com", "admin"},
		{"unknown role", "other@example.com", "owner"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := conn.Exec(t.Context(), insert, c.email, c.role)

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Equal(t, checkViolation, pgErr.Code)
		})
	}
}

// Emails are unique exactly.
func TestUsersEmailUnique(t *testing.T) {
	const uniqueViolation = "23505"
	conn := migrated(t)
	const insert = "INSERT INTO users (email, password_hash, role) VALUES ($1, $2, 'member')"
	_, err := conn.Exec(t.Context(), insert, "admin@example.com", setupTestHash)
	require.NoError(t, err)

	_, err = conn.Exec(t.Context(), insert, "admin@example.com", setupTestHash)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, uniqueViolation, pgErr.Code)
}

// Only ASCII letters must be lowercase, so non-ASCII case variants are
// different addresses.
func TestUsersEmailNonASCIICase(t *testing.T) {
	conn := migrated(t)
	const insert = "INSERT INTO users (email, password_hash, role) VALUES ($1, $2, 'member')"
	_, err := conn.Exec(t.Context(), insert, "élise@example.fr", setupTestHash)
	require.NoError(t, err)

	_, err = conn.Exec(t.Context(), insert, "Élise@example.fr", setupTestHash)

	require.NoError(t, err)
	assert.Equal(t, int64(2), countUsers(t, conn))
}

func TestUsersMigrationDown(t *testing.T) {
	const beforeUsersMigration = 1
	const exists = "SELECT to_regclass('public.users') IS NOT NULL"
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())

	_, err = provider.DownTo(t.Context(), beforeUsersMigration)
	require.NoError(t, err)
	var present bool
	require.NoError(t, conn.QueryRow(t.Context(), exists).Scan(&present))
	assert.False(t, present)

	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, conn.QueryRow(t.Context(), exists).Scan(&present))
	assert.True(t, present)
}

func TestReadSetup(t *testing.T) {
	db, conn := setupStorage(t)
	const insert = "INSERT INTO users (email, password_hash, role) VALUES ($1, $2, $3)"

	state, err := db.ReadSetup(t.Context())
	require.NoError(t, err)
	assert.Equal(t, account.SetupState{}, state)

	_, err = conn.Exec(t.Context(), insert, "member@example.com", setupTestHash, "member")
	require.NoError(t, err)
	state, err = db.ReadSetup(t.Context())
	require.NoError(t, err)
	assert.Equal(t, account.SetupState{}, state, "a member does not finish setup")

	_, err = conn.Exec(t.Context(), insert, "admin@example.com", setupTestHash, "admin")
	require.NoError(t, err)
	state, err = db.ReadSetup(t.Context())
	require.NoError(t, err)
	assert.Equal(t, account.SetupState{AdminExists: true}, state)
}

func TestUpdateSetup(t *testing.T) {
	db, conn := setupStorage(t)
	service := account.NewService(db, setupTestToken)

	user, err := service.Setup(t.Context(), account.SetupInput{
		Token: setupTestToken, Email: " ADMIN@Example.COM ", Password: "a long setup password",
	})

	require.NoError(t, err)
	require.Positive(t, user.ID)
	assert.Equal(t, "admin@example.com", user.Email)
	var email, hash, role string
	var createdAt time.Time
	err = conn.QueryRow(t.Context(), "SELECT email, password_hash, role, created_at FROM users WHERE id = $1", user.ID).
		Scan(&email, &hash, &role, &createdAt)
	require.NoError(t, err)
	assert.Equal(t, user.Email, email)
	assert.Regexp(t, `^\$argon2id\$v=19\$`, hash)
	assert.Equal(t, "admin", role)
	assert.False(t, createdAt.IsZero())

	status, err := service.SetupStatus(t.Context())
	require.NoError(t, err)
	assert.Equal(t, account.SetupDone, status)
}

// Keep the first claim open and observe the second waiting on PostgreSQL's
// table lock. After commit, the second callback must see the new admin.
func TestUpdateSetupSerializes(t *testing.T) {
	db, conn := setupStorage(t)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()
	ready, release := make(chan struct{}), make(chan struct{})
	releaseFirst := sync.OnceFunc(func() { close(release) })
	defer releaseFirst()
	firstDone, secondDone := make(chan setupResult, 1), make(chan setupResult, 1)

	go func() {
		user, err := db.UpdateSetup(ctx, func(account.SetupState) (account.NewAdmin, error) {
			close(ready)
			select {
			case <-release:
				return account.NewAdmin{Email: "first@example.com", PasswordHash: setupTestHash}, nil
			case <-ctx.Done():
				return account.NewAdmin{}, ctx.Err()
			}
		})
		firstDone <- setupResult{user: user, err: err}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		require.FailNow(t, "first claim did not reach its callback")
	}

	state, err := db.ReadSetup(ctx)
	require.NoError(t, err, "status reads must remain available while claiming")
	assert.False(t, state.AdminExists)

	go func() {
		user, err := db.UpdateSetup(ctx, func(state account.SetupState) (account.NewAdmin, error) {
			if state.AdminExists {
				return account.NewAdmin{}, account.ErrSetupDone
			}
			return account.NewAdmin{Email: "second@example.com", PasswordHash: setupTestHash}, nil
		})
		secondDone <- setupResult{user: user, err: err}
	}()

	const waiting = `SELECT EXISTS (
		SELECT 1 FROM pg_locks
		WHERE relation = 'users'::regclass AND mode = 'ShareRowExclusiveLock' AND NOT granted
		  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
	)`
	require.Eventually(t, func() bool {
		var blocked bool
		err := conn.QueryRow(ctx, waiting).Scan(&blocked)
		return err == nil && blocked
	}, setupTestTimeout, 10*time.Millisecond, "second claim did not wait on the table lock")

	releaseFirst()
	first, second := <-firstDone, <-secondDone
	require.NoError(t, first.err)
	assert.Equal(t, "first@example.com", first.user.Email)
	require.ErrorIs(t, second.err, account.ErrSetupDone)
	assert.Zero(t, second.user)
	assert.Equal(t, int64(1), countUsers(t, conn))
}

func TestSetupConcurrent(t *testing.T) {
	db, conn := setupStorage(t)
	service := account.NewService(db, setupTestToken)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()
	emails := []string{"first@example.com", "second@example.com"}
	results := make([]setupResult, len(emails))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, email := range emails {
		wg.Go(func() {
			<-start
			results[i].user, results[i].err = service.Setup(ctx, account.SetupInput{
				Token: setupTestToken, Email: email, Password: "a long setup password",
			})
		})
	}

	close(start)
	wg.Wait()

	winners := 0
	for _, result := range results {
		if result.err == nil {
			winners++
			assert.Positive(t, result.user.ID)
			continue
		}
		require.ErrorIs(t, result.err, account.ErrSetupDone)
		assert.Zero(t, result.user)
	}
	assert.Equal(t, 1, winners)
	assert.Equal(t, int64(1), countUsers(t, conn))
}

func TestUpdateSetupRejected(t *testing.T) {
	db, conn := setupStorage(t)
	rejected := errors.New("claim rejected")

	user, err := db.UpdateSetup(t.Context(), func(account.SetupState) (account.NewAdmin, error) {
		return account.NewAdmin{Email: "admin@example.com", PasswordHash: setupTestHash}, rejected
	})

	require.ErrorIs(t, err, rejected)
	assert.Zero(t, user)
	assert.Equal(t, int64(0), countUsers(t, conn))
}

// The insert succeeds, but a deferred trigger rejects commit. No returned
// user or saved row may survive, and a later claim must still work.
func TestUpdateSetupCommitFailure(t *testing.T) {
	db, conn := setupStorage(t)
	_, err := conn.Exec(t.Context(), `
		CREATE FUNCTION reject_setup() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'forced setup failure'; END $$;
		CREATE CONSTRAINT TRIGGER reject_setup AFTER INSERT ON users
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_setup();`)
	require.NoError(t, err)
	service := account.NewService(db, setupTestToken)
	input := account.SetupInput{Token: setupTestToken, Email: "admin@example.com", Password: "a long setup password"}

	user, err := service.Setup(t.Context(), input)

	require.ErrorContains(t, err, "forced setup failure")
	assert.Zero(t, user)
	assert.Equal(t, int64(0), countUsers(t, conn))
	status, err := service.SetupStatus(t.Context())
	require.NoError(t, err)
	assert.Equal(t, account.SetupOpen, status)

	_, err = conn.Exec(t.Context(), "DROP TRIGGER reject_setup ON users")
	require.NoError(t, err)
	user, err = service.Setup(t.Context(), input)
	require.NoError(t, err)
	assert.Positive(t, user.ID)
	assert.Equal(t, int64(1), countUsers(t, conn))
}
