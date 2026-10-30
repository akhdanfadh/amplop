package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/encryption"
	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/session"
	"inbok/internal/totp"
)

// RFC 6238's SHA-1 secret matches code 287082 at Unix second 59.
const confirmationSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

type confirmationFixture struct {
	db         *DB
	conn       *pgx.Conn
	service    *session.Enrollment
	sessions   *session.Service
	current    session.Authenticated
	state      session.State
	other      session.State
	foreign    session.State
	factor     *totp.Factor
	attemptIDs []int64
	now        time.Time
}

func newConfirmation(t *testing.T) *confirmationFixture {
	t.Helper()
	db, conn := setupStorage(t)
	user, err := db.UpdateSetup(t.Context(), func(account.SetupState) (account.NewAdmin, error) {
		return account.NewAdmin{Email: "admin@example.com", PasswordHash: setupTestHash}, nil
	})
	require.NoError(t, err)
	require.Positive(t, user.ID)
	var otherUser int64
	require.NoError(t, conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ('other@example.com', 'unused', 'member') RETURNING id").Scan(&otherUser))
	require.Positive(t, otherUser)
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	keyID, ciphertext, err := ring.Seal([]byte(confirmationSecret), "totp:"+strconv.FormatInt(user.ID, 10))
	require.NoError(t, err)
	require.NoError(t, db.UpdateTOTP(t.Context(), user.ID, func(*totp.Factor) (totp.Factor, error) {
		return totp.Factor{KeyID: keyID, Ciphertext: ciphertext, LastStep: -1}, nil
	}))

	now := time.Unix(59, 0).UTC()
	clock := func() time.Time { return now }
	require.NoError(t, db.UpdateTOTP(t.Context(), otherUser, func(*totp.Factor) (totp.Factor, error) {
		return totp.Factor{KeyID: "1", Ciphertext: []byte("other ciphertext"), ConfirmedAt: &now, LastStep: 1}, nil
	}))
	require.NoError(t, sqlc.New(conn).InsertTOTPBackupCode(t.Context(), sqlc.InsertTOTPBackupCodeParams{UserID: otherUser, CodeHash: setupTestHash}))

	// This cookie is the base64url encoding of 32 zero bytes.
	const cookie = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	state := session.State{TokenHash: sha256.Sum256(make([]byte, 32)), UserID: user.ID, CreatedAt: now, LastUsedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	other, foreign := state, state
	other.TokenHash = sha256.Sum256([]byte("another session"))
	foreign.TokenHash, foreign.UserID = sha256.Sum256([]byte("another user's session")), otherUser
	for _, row := range []session.State{state, other, foreign} {
		require.NoError(t, db.InsertSession(t.Context(), row))
	}
	sessions := session.NewService(db, account.NewService(db, ""), nil, clock)
	current, err := sessions.Current(t.Context(), cookie)
	require.NoError(t, err)
	factor, err := db.ReadTOTP(t.Context(), user.ID)
	require.NoError(t, err)
	require.NotNil(t, factor)
	throttle := totp.NewThrottle(db, clock, slog.New(slog.DiscardHandler))
	f := &confirmationFixture{db: db, conn: conn, sessions: sessions, current: current, state: state, other: other, foreign: foreign, factor: factor, now: now}
	f.service = session.NewEnrollment(db, ring, totp.NewVerifier(clock), throttle, clock)
	for range 2 {
		id, err := throttle.Begin(t.Context(), user.ID)
		require.NoError(t, err)
		f.attemptIDs = append(f.attemptIDs, id)
	}
	return f
}

func assertPendingConfirmation(t *testing.T, f *confirmationFixture) {
	t.Helper()
	factor, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Equal(t, f.factor, factor)
	rows, err := sqlc.New(f.conn).ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Empty(t, rows)
	for _, row := range []session.State{f.state, f.other, f.foreign} {
		assert.Equal(t, row, readSession(t, f.db, row.TokenHash))
	}
}

func TestTOTPConfirmationStorage(t *testing.T) {
	f := newConfirmation(t)
	device := session.Device{UserAgent: "confirming browser", Addr: netip.MustParseAddr("2001:db8::5")}

	result, err := f.service.Confirm(t.Context(), f.current, "287082", device)
	require.NoError(t, err)
	assert.Equal(t, device, result.Session.State.Device)

	factor, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
	require.NoError(t, err)
	require.NotNil(t, factor)
	require.NotNil(t, factor.ConfirmedAt)
	assert.WithinDuration(t, f.now, *factor.ConfirmedAt, time.Microsecond)
	assert.Equal(t, int64(1), factor.LastStep)
	assert.Equal(t, f.factor.Ciphertext, factor.Ciphertext)
	rows, err := sqlc.New(f.conn).ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	require.Len(t, rows, 10)
	require.Len(t, result.BackupCodes, len(rows))
	for i, row := range rows {
		require.NoError(t, totp.CheckBackupCode(result.BackupCodes[i], row.CodeHash))
	}
	assert.Equal(t, result.Session.State, readSession(t, f.db, result.Session.State.TokenHash))
	current, err := f.sessions.Current(t.Context(), result.Session.Secret)
	require.NoError(t, err)
	assert.Equal(t, f.state.UserID, current.Identity.User.ID)
	for _, hash := range [][sha256.Size]byte{f.state.TokenHash, f.other.TokenHash} {
		err := f.db.UpdateSession(t.Context(), hash, func(session.State) (session.Change, error) {
			t.Error("old session survived confirmation")
			return session.Change{}, nil
		})
		require.ErrorIs(t, err, session.ErrNotFound)
	}
	assert.Equal(t, f.foreign, readSession(t, f.db, f.foreign.TokenHash))
	foreignHashes, err := sqlc.New(f.conn).ReadTOTPBackupCodes(t.Context(), f.foreign.UserID)
	require.NoError(t, err)
	require.Len(t, foreignHashes, 1)
	assert.Equal(t, setupTestHash, foreignHashes[0].CodeHash)
	var count int
	require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM totp_attempts WHERE user_id = $1", f.state.UserID).Scan(&count))
	assert.Equal(t, len(f.attemptIDs), count, "success removes only its own reservation")
	for _, id := range f.attemptIDs {
		var present bool
		require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM totp_attempts WHERE id = $1)", id).Scan(&present))
		assert.True(t, present)
	}
}

func TestTOTPConfirmationRollback(t *testing.T) {
	for _, c := range []struct {
		name, trigger string
	}{
		{"backup insert", "CREATE TRIGGER reject_write BEFORE INSERT ON totp_backup_codes FOR EACH ROW EXECUTE FUNCTION reject_confirmation()"},
		{"session insert", "CREATE TRIGGER reject_write BEFORE INSERT ON sessions FOR EACH ROW EXECUTE FUNCTION reject_confirmation()"},
		{"attempt removal", "CREATE TRIGGER reject_write BEFORE DELETE ON totp_attempts FOR EACH ROW EXECUTE FUNCTION reject_confirmation()"},
		{"commit", "CREATE CONSTRAINT TRIGGER reject_write AFTER INSERT ON totp_backup_codes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_confirmation()"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newConfirmation(t)
			_, err := f.conn.Exec(t.Context(), `CREATE FUNCTION reject_confirmation() RETURNS trigger LANGUAGE plpgsql AS
				$$ BEGIN RAISE EXCEPTION 'forced confirmation failure'; END $$`)
			require.NoError(t, err)
			_, err = f.conn.Exec(t.Context(), c.trigger)
			require.NoError(t, err)

			result, err := f.service.Confirm(t.Context(), f.current, "287082", session.Device{})

			require.ErrorContains(t, err, "forced confirmation failure")
			assert.Empty(t, result, "no new secrets may escape a failed write or commit")
			assertPendingConfirmation(t, f)
			var count int
			require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM totp_attempts WHERE user_id = $1", f.state.UserID).Scan(&count))
			assert.Equal(t, len(f.attemptIDs)+1, count, "the failed confirmation remains counted")
		})
	}
}

func TestTOTPConfirmationRejected(t *testing.T) {
	f := newConfirmation(t)
	rejected := errors.New("confirmation rejected")
	err := f.db.ConfirmTOTP(t.Context(), f.state.UserID, f.state.TokenHash, func(state session.EnrollmentState) (session.EnrollmentChange, error) {
		assert.Equal(t, f.state, state.Session)
		assert.Equal(t, f.factor, state.Factor)
		return session.EnrollmentChange{}, rejected
	})
	require.ErrorIs(t, err, rejected)
	assertPendingConfirmation(t, f)

	for _, c := range []struct {
		userID int64
		hash   [sha256.Size]byte
	}{
		{f.state.UserID + 1000, f.state.TokenHash},
		{f.foreign.UserID, f.state.TokenHash},
		{f.state.UserID, [sha256.Size]byte{}},
	} {
		err := f.db.ConfirmTOTP(t.Context(), c.userID, c.hash, func(session.EnrollmentState) (session.EnrollmentChange, error) {
			t.Error("missing or wrongly owned session reached callback")
			return session.EnrollmentChange{}, nil
		})
		require.ErrorIs(t, err, session.ErrNotFound)
	}
}

func TestTOTPConfirmationParallel(t *testing.T) {
	f := newConfirmation(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	type result struct {
		value session.Confirmed
		err   error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			value, err := f.service.Confirm(ctx, f.current, "287082", session.Device{})
			results <- result{value, err}
		}()
	}
	close(start)

	var accepted, denied int
	for range 2 {
		r := <-results
		if errors.Is(r.err, session.ErrUnauthenticated) {
			assert.Empty(t, r.value)
			denied++
			continue
		}
		require.NoError(t, r.err)
		accepted++
	}
	assert.Equal(t, 1, accepted)
	assert.Equal(t, 1, denied)
	var sessions, hashes, attempts int
	err := f.conn.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM sessions WHERE user_id = $1),
		(SELECT count(*) FROM totp_backup_codes WHERE user_id = $1),
		(SELECT count(*) FROM totp_attempts WHERE user_id = $1)`, f.state.UserID).Scan(&sessions, &hashes, &attempts)
	require.NoError(t, err)
	assert.Equal(t, 1, sessions)
	assert.Equal(t, 10, hashes)
	assert.Equal(t, len(f.attemptIDs)+1, attempts)
}

func TestSessionUserIndexDown(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())
	_, err = provider.DownTo(t.Context(), 7)
	require.NoError(t, err)
	var present bool
	const exists = "SELECT to_regclass('public.sessions_user_idx') IS NOT NULL"
	require.NoError(t, conn.QueryRow(t.Context(), exists).Scan(&present))
	assert.False(t, present)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, conn.QueryRow(t.Context(), exists).Scan(&present))
	assert.True(t, present)
}
