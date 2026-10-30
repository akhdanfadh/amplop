package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/session"
)

func sessionStorage(t *testing.T) (*DB, session.State) {
	t.Helper()
	db, _ := setupStorage(t)
	user, err := db.UpdateSetup(t.Context(), func(account.SetupState) (account.NewAdmin, error) {
		return account.NewAdmin{Email: "admin@example.com", PasswordHash: setupTestHash}, nil
	})
	require.NoError(t, err)
	created := time.Date(2020, time.January, 1, 12, 0, 0, 0, time.UTC)
	return db, session.State{
		TokenHash: sha256.Sum256([]byte("test-only-session-secret")), UserID: user.ID,
		CreatedAt: created, LastUsedAt: created, ExpiresAt: created.Add(30 * 24 * time.Hour),
	}
}

// readSession omits the storage-assigned ID, so a read row equals the
// state it was inserted from.
func readSession(t *testing.T, db *DB, hash [sha256.Size]byte) session.State {
	t.Helper()
	var state session.State
	err := db.UpdateSession(t.Context(), hash, func(stored session.State) (session.Change, error) {
		state = stored
		return session.Change{Action: session.Keep}, nil
	})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil(), state.ID)
	state.ID = uuid.Nil()
	return state
}

func TestSessionStorage(t *testing.T) {
	db, state := sessionStorage(t)

	require.NoError(t, db.InsertSession(t.Context(), state))
	assert.Equal(t, state, readSession(t, db, state.TokenHash))

	touched := state.LastUsedAt.Add(time.Hour)
	err := db.UpdateSession(t.Context(), state.TokenHash, func(session.State) (session.Change, error) {
		return session.Change{Action: session.Touch, LastUsedAt: touched}, nil
	})
	require.NoError(t, err)
	state.LastUsedAt = touched
	assert.Equal(t, state, readSession(t, db, state.TokenHash))

	require.NoError(t, db.DeleteSession(t.Context(), state.TokenHash))
	require.NoError(t, db.DeleteSession(t.Context(), state.TokenHash))
	err = db.UpdateSession(t.Context(), state.TokenHash, func(session.State) (session.Change, error) {
		t.Error("missing session reached update")
		return session.Change{}, nil
	})
	require.ErrorIs(t, err, session.ErrNotFound)
}

func TestSessionUpdateRollback(t *testing.T) {
	db, state := sessionStorage(t)
	require.NoError(t, db.InsertSession(t.Context(), state))
	rejected := errors.New("update rejected")
	err := db.UpdateSession(t.Context(), state.TokenHash, func(session.State) (session.Change, error) {
		return session.Change{Action: session.Delete}, rejected
	})
	require.ErrorIs(t, err, rejected)
	assert.Equal(t, state, readSession(t, db, state.TokenHash))

	err = db.UpdateSession(t.Context(), state.TokenHash, func(session.State) (session.Change, error) {
		return session.Change{Action: session.Action(-1)}, nil
	})
	require.EqualError(t, err, "invalid session action")
	assert.Equal(t, state, readSession(t, db, state.TokenHash))
}

func TestSessionExpiredDecision(t *testing.T) {
	db, state := sessionStorage(t)
	require.NoError(t, db.InsertSession(t.Context(), state))
	called := false

	err := db.UpdateSession(t.Context(), state.TokenHash, func(stored session.State) (session.Change, error) {
		called = true
		// Expired rows still reach the Go rule; SQL must not filter them out.
		assert.Equal(t, state.ExpiresAt, stored.ExpiresAt)
		return session.Change{Action: session.Delete}, nil
	})
	require.NoError(t, err)
	assert.True(t, called)
	err = db.UpdateSession(t.Context(), state.TokenHash, func(session.State) (session.Change, error) {
		t.Error("deleted session reached update")
		return session.Change{}, nil
	})
	require.ErrorIs(t, err, session.ErrNotFound)
}

func TestSessionConcurrentTouch(t *testing.T) {
	const requests = 12
	db, state := sessionStorage(t)
	require.NoError(t, db.InsertSession(t.Context(), state))
	now := state.LastUsedAt.Add(time.Hour)
	var touches atomic.Int32
	start := make(chan struct{})
	errs := make(chan error, requests)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()
	var group sync.WaitGroup
	for range requests {
		group.Go(func() {
			<-start
			errs <- db.UpdateSession(ctx, state.TokenHash, func(stored session.State) (session.Change, error) {
				if now.Sub(stored.LastUsedAt) < time.Hour {
					return session.Change{Action: session.Keep}, nil
				}
				touches.Add(1)
				return session.Change{Action: session.Touch, LastUsedAt: now}, nil
			})
		})
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	assert.Equal(t, int32(1), touches.Load())
	state.LastUsedAt = now
	assert.Equal(t, state, readSession(t, db, state.TokenHash))
}

func TestSessionInsertConstraints(t *testing.T) {
	const duplicateKey = "23505"
	const foreignKey = "23503"
	db, state := sessionStorage(t)
	require.NoError(t, db.InsertSession(t.Context(), state))

	err := db.InsertSession(t.Context(), state)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, duplicateKey, pgErr.Code)

	state.TokenHash = sha256.Sum256([]byte("another-test-secret"))
	state.UserID = -1
	err = db.InsertSession(t.Context(), state)
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, foreignKey, pgErr.Code)
}

func TestSessionsMigrationDown(t *testing.T) {
	const beforeSessions = 2
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())

	_, err = provider.DownTo(t.Context(), beforeSessions)
	require.NoError(t, err)
	var sessions, users bool
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT to_regclass('public.sessions') IS NOT NULL, to_regclass('public.users') IS NOT NULL").Scan(&sessions, &users))
	assert.False(t, sessions)
	assert.True(t, users)

	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT to_regclass('public.sessions') IS NOT NULL").Scan(&sessions))
	assert.True(t, sessions)
}

func TestSessionDeviceStorage(t *testing.T) {
	db, state := sessionStorage(t)
	withDevice := state
	withDevice.Device = session.Device{UserAgent: "Mozilla/5.0 Firefox/131.0", Addr: netip.MustParseAddr("2001:db8::1")}
	withoutDevice := state
	withoutDevice.TokenHash = sha256.Sum256([]byte("no device"))

	for _, row := range []session.State{withDevice, withoutDevice} {
		require.NoError(t, db.InsertSession(t.Context(), row))
	}

	assert.Equal(t, withDevice, readSession(t, db, withDevice.TokenHash))
	assert.Equal(t, withoutDevice, readSession(t, db, withoutDevice.TokenHash))
	var agents, addrs int
	require.NoError(t, db.pool.QueryRow(t.Context(), "SELECT count(user_agent), count(signed_in_from) FROM sessions").Scan(&agents, &addrs))
	assert.Equal(t, 1, agents, "an empty User-Agent is stored as NULL")
	assert.Equal(t, 1, addrs, "an unknown address is stored as NULL")

	_, err := db.pool.Exec(t.Context(), `INSERT INTO sessions (token_hash, user_id, expires_at, user_agent)
		VALUES ('\x01', $1, now() + interval '1 day', '')`, state.UserID)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, checkViolation, pgErr.Code, "empty User-Agent")
}

// currentCookie is the base64url encoding of 32 zero bytes, the secret of
// userSessions' current session.
var currentCookie = base64.RawURLEncoding.EncodeToString(make([]byte, 32))

// userSessions holds user A's current, other, and expired sessions, and user
// B's session, all inserted through InsertSession.
type userSessions struct {
	db                               *DB
	current, other, expired, foreign session.State
}

func newUserSessions(t *testing.T) userSessions {
	t.Helper()
	db, state := sessionStorage(t)
	var otherUser int64
	require.NoError(t, db.pool.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ('other@example.com', 'unused', 'member') RETURNING id").Scan(&otherUser))

	now := time.Now().UTC().Truncate(time.Microsecond)
	f := userSessions{db: db, current: state}
	f.current.TokenHash = sha256.Sum256(make([]byte, 32))
	f.current.CreatedAt, f.current.LastUsedAt, f.current.ExpiresAt = now.Add(-2*time.Hour), now.Add(-2*time.Hour), now.Add(29*24*time.Hour)
	f.other, f.expired, f.foreign = f.current, f.current, f.current
	f.other.TokenHash = sha256.Sum256([]byte("other"))
	f.other.CreatedAt, f.other.LastUsedAt = now.Add(-time.Hour), now.Add(-time.Hour)
	f.other.Device = session.Device{UserAgent: "Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0", Addr: netip.MustParseAddr("192.0.2.9")}
	f.expired.TokenHash = sha256.Sum256([]byte("expired"))
	f.expired.LastUsedAt = now.Add(-8 * 24 * time.Hour)
	f.foreign.TokenHash, f.foreign.UserID = sha256.Sum256([]byte("foreign")), otherUser
	for _, row := range []*session.State{&f.current, &f.other, &f.expired, &f.foreign} {
		require.NoError(t, db.InsertSession(t.Context(), *row))
		var id uuid.UUID
		require.NoError(t, db.pool.QueryRow(t.Context(), "SELECT id FROM sessions WHERE token_hash = $1", row.TokenHash[:]).Scan(&id))
		row.ID = id
	}
	return f
}

// ids lists the user's stored session IDs.
func (f userSessions) ids(t *testing.T, userID int64) []uuid.UUID {
	t.Helper()
	rows, err := f.db.ReadUserSessions(t.Context(), userID)
	require.NoError(t, err)
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func TestReadUserSessions(t *testing.T) {
	f := newUserSessions(t)

	rows, err := f.db.ReadUserSessions(t.Context(), f.current.UserID)

	require.NoError(t, err)
	// current and expired signed in at the same time; the higher ID comes first.
	tied := []session.State{f.current, f.expired}
	if f.current.ID.Compare(f.expired.ID) < 0 {
		tied = []session.State{f.expired, f.current}
	}
	assert.Equal(t, append([]session.State{f.other}, tied...), rows, "newest sign-in first, expired rows included")
	missing, err := f.db.ReadUserSessions(t.Context(), -1)
	require.NoError(t, err)
	assert.Empty(t, missing)
}

func TestListPersistence(t *testing.T) {
	f := newUserSessions(t)
	service := session.NewService(f.db, nil, nil, time.Now)
	current, err := service.Current(t.Context(), currentCookie)
	require.NoError(t, err)

	listed, err := service.List(t.Context(), current)

	require.NoError(t, err)
	require.Len(t, listed, 2, "the expired session is hidden")
	assert.Equal(t, session.Listed{
		ID: f.other.ID, Browser: "Firefox", OS: "Linux", SignedInFrom: f.other.Device.Addr,
		CreatedAt: f.other.CreatedAt, LastUsedAt: f.other.LastUsedAt,
	}, listed[0])
	assert.Equal(t, f.current.ID, listed[1].ID)
	assert.True(t, listed[1].Current)
	assert.Empty(t, listed[1].Browser, "a session without details is an unknown browser")
	assert.ElementsMatch(t, []uuid.UUID{f.current.ID, f.other.ID, f.expired.ID}, f.ids(t, f.current.UserID), "listing deletes nothing")
}

func TestUpdateUserSession(t *testing.T) {
	f := newUserSessions(t)

	// Another user's ID and a missing ID never reach the callback.
	for _, id := range []uuid.UUID{f.foreign.ID, uuid.New()} {
		err := f.db.UpdateUserSession(t.Context(), f.current.UserID, id, func(session.State) (session.Change, error) {
			t.Error("another user's or a missing session reached update")
			return session.Change{Action: session.Delete}, nil
		})
		require.ErrorIs(t, err, session.ErrNotFound)
	}

	err := f.db.UpdateUserSession(t.Context(), f.current.UserID, f.other.ID, func(stored session.State) (session.Change, error) {
		assert.Equal(t, f.other, stored)
		return session.Change{Action: session.Delete}, nil
	})

	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{f.current.ID, f.expired.ID}, f.ids(t, f.current.UserID))
	assert.Equal(t, []uuid.UUID{f.foreign.ID}, f.ids(t, f.foreign.UserID))
}

func TestEndPersistence(t *testing.T) {
	f := newUserSessions(t)
	service := session.NewService(f.db, nil, nil, time.Now)
	current, err := service.Current(t.Context(), currentCookie)
	require.NoError(t, err)

	// When the user ends sessions one at a time.
	foreign := service.End(t.Context(), current, f.foreign.ID)
	own := service.End(t.Context(), current, f.current.ID)
	expired := service.End(t.Context(), current, f.expired.ID)
	other := service.End(t.Context(), current, f.other.ID)
	again := service.End(t.Context(), current, f.other.ID)

	// Then only the other live session ends, and the expired row is cleared.
	require.ErrorIs(t, foreign, session.ErrNotFound)
	require.ErrorIs(t, own, session.ErrCurrentSession)
	require.ErrorIs(t, expired, session.ErrNotFound)
	require.NoError(t, other)
	require.ErrorIs(t, again, session.ErrNotFound, "ending twice finds nothing")
	assert.Equal(t, []uuid.UUID{f.current.ID}, f.ids(t, f.current.UserID))
	assert.Equal(t, []uuid.UUID{f.foreign.ID}, f.ids(t, f.foreign.UserID))
}

func TestSessionDevicesMigration(t *testing.T) {
	// Renumber with the migration if another branch takes 00011 first.
	const beforeDevices = 10
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())
	_, err = provider.DownTo(t.Context(), beforeDevices)
	require.NoError(t, err)
	var userID int64
	require.NoError(t, conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ('a@example.com', 'unused', 'member') RETURNING id").Scan(&userID))
	_, err = conn.Exec(t.Context(), `INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES ('\x01', $1, now() + interval '1 day')`, userID)
	require.NoError(t, err)

	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	var noAgent, noAddr bool
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT user_agent IS NULL, signed_in_from IS NULL FROM sessions").Scan(&noAgent, &noAddr))
	assert.True(t, noAgent, "existing rows keep no details")
	assert.True(t, noAddr)

	_, err = provider.DownTo(t.Context(), beforeDevices)
	require.NoError(t, err)
	var columns int
	require.NoError(t, conn.QueryRow(t.Context(), `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'sessions' AND column_name IN ('user_agent', 'signed_in_from')`).Scan(&columns))
	assert.Zero(t, columns)
}

func TestSessionIDsMigration(t *testing.T) {
	// Renumber with the migration if another branch takes 00012 first.
	const beforeIDs = 11
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())
	_, err = provider.DownTo(t.Context(), beforeIDs)
	require.NoError(t, err)
	var userID int64
	require.NoError(t, conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ('a@example.com', 'unused', 'member') RETURNING id").Scan(&userID))
	_, err = conn.Exec(t.Context(), `INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES ('\x01', $1, now() + interval '1 day'), ('\x02', $1, now() + interval '1 day')`, userID)
	require.NoError(t, err)

	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	rows, err := conn.Query(t.Context(), "SELECT id FROM sessions ORDER BY id")
	require.NoError(t, err)
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	require.NoError(t, err)
	require.Len(t, ids, 2)
	assert.NotContains(t, ids, uuid.Nil())
	assert.NotEqual(t, ids[0], ids[1], "each existing row gets its own ID")

	_, err = provider.DownTo(t.Context(), beforeIDs)
	require.NoError(t, err)
	var columns int
	require.NoError(t, conn.QueryRow(t.Context(), `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'sessions' AND column_name = 'id'`).Scan(&columns))
	assert.Zero(t, columns)
}
