package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/encryption"
	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/totp"
)

// testKeyring is an encryption keyring with one fixed test key.
func testKeyring(t *testing.T) *encryption.Keyring {
	t.Helper()
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	return ring
}

// saveFactor saves factor for userID through [DB.UpdateTOTP].
func saveFactor(ctx context.Context, db *DB, userID int64, factor totp.Factor) error {
	return db.UpdateTOTP(ctx, userID, func(totp.Record) (totp.Change, error) {
		return totp.Change{Factor: &factor}, nil
	})
}

// acceptPasswords is a password check that accepts every password.
type acceptPasswords struct{}

func (acceptPasswords) VerifyPassword(context.Context, account.User, string, netip.Addr) error {
	return nil
}

// readWaitingCodes returns userID's waiting backup-code set, nil when none.
func readWaitingCodes(t *testing.T, conn *pgx.Conn, userID int64) *totp.Waiting {
	t.Helper()
	waiting, err := readWaiting(t.Context(), sqlc.New(conn), userID)
	require.NoError(t, err)
	return waiting
}

// newTOTPService is the enrollment service on db at clock, accepting every
// password.
func newTOTPService(t *testing.T, db *DB, clock func() time.Time) *totp.Service {
	t.Helper()
	throttle := totp.NewThrottle(db, clock, slog.New(slog.DiscardHandler))
	return totp.NewService(db, testKeyring(t), acceptPasswords{}, totp.NewVerifier(clock), throttle, clock)
}

func TestTOTPEnrollmentStorage(t *testing.T) {
	db, conn := setupStorage(t)
	var id int64
	require.NoError(t, conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ('alice@example.com', 'unused', 'member') RETURNING id").Scan(&id))
	require.Positive(t, id)
	ring := testKeyring(t)
	now := time.Unix(1_000_000, 0).UTC()
	clock := func() time.Time { return now }
	service := newTOTPService(t, db, clock)
	alice := account.User{ID: id, Email: "alice@example.com"}
	addr := netip.MustParseAddr("192.0.2.1")
	status, err := service.Status(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, totp.StateOff, status.State)

	first, err := service.Start(t.Context(), alice, "password", addr)
	require.NoError(t, err)
	factor, err := db.ReadTOTP(t.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, factor)
	assert.NotContains(t, string(factor.Ciphertext), first.Secret)
	opened, err := ring.Open(factor.KeyID, factor.Ciphertext, "totp:"+fmt.Sprint(id))
	require.NoError(t, err)
	assert.Equal(t, first.Secret, string(opened))
	assert.Nil(t, factor.ConfirmedAt)
	assert.Equal(t, int64(-1), factor.LastStep)
	assert.True(t, now.Equal(factor.StartedAt))
	status, err = service.Status(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, totp.Status{State: totp.StatePending}, status)

	// Continuing returns the saved secret and changes nothing.
	second, err := service.Start(t.Context(), alice, "password", addr)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	unchanged, err := db.ReadTOTP(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, factor, unchanged)

	// An expired enrollment is replaced.
	now = now.Add(15 * time.Minute)
	status, err = service.Status(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, totp.StateExpired, status.State)
	third, err := service.Start(t.Context(), alice, "password", addr)
	require.NoError(t, err)
	assert.NotEqual(t, first.Secret, third.Secret)
	factor, err = db.ReadTOTP(t.Context(), id)
	require.NoError(t, err)
	opened, err = ring.Open(factor.KeyID, factor.Ciphertext, "totp:"+fmt.Sprint(id))
	require.NoError(t, err)
	assert.Equal(t, third.Secret, string(opened))

	// A callback error rolls back a replacement.
	rejected := errors.New("replacement rejected")
	err = db.UpdateTOTP(t.Context(), id, func(totp.Record) (totp.Change, error) { return totp.Change{}, rejected })
	require.ErrorIs(t, err, rejected)
	unchanged, err = db.ReadTOTP(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, factor, unchanged)

	_, err = conn.Exec(t.Context(), "UPDATE user_totp SET confirmed_at = now(), last_step = 12 WHERE user_id = $1", id)
	require.NoError(t, err)
	result, err := service.Start(t.Context(), alice, "password", addr)
	require.ErrorIs(t, err, totp.ErrAlreadyOn)
	assert.Empty(t, result)
	factor, err = db.ReadTOTP(t.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, factor)
	assert.NotNil(t, factor.ConfirmedAt)
	assert.Equal(t, int64(12), factor.LastStep)
}

func TestTOTPWaitingCodesStorage(t *testing.T) {
	db, conn := setupStorage(t)
	alice := account.User{Email: "alice@example.com"}
	require.NoError(t, conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ($1, 'unused', 'member') RETURNING id", alice.Email).Scan(&alice.ID))
	ring := testKeyring(t)
	now := time.Unix(59, 0).UTC()
	clock := func() time.Time { return now }
	service := newTOTPService(t, db, clock)
	keyID, ciphertext, err := ring.Seal([]byte(confirmationSecret), "totp:"+fmt.Sprint(alice.ID))
	require.NoError(t, err)
	require.NoError(t, saveFactor(t.Context(), db, alice.ID, totp.Factor{KeyID: keyID, Ciphertext: ciphertext, LastStep: -1, StartedAt: now}))

	codes, err := service.VerifyCode(t.Context(), alice.ID, "287082")
	require.NoError(t, err)

	waiting := readWaitingCodes(t, conn, alice.ID)
	require.NotNil(t, waiting)
	require.Len(t, waiting.Hashes, 10)
	for i, code := range codes.Codes {
		require.NoError(t, totp.VerifyBackupCode(t.Context(), code, waiting.Hashes[i]))
	}
	copied, err := ring.Open(waiting.KeyID, waiting.Ciphertext, "totp-backup-codes:"+fmt.Sprint(alice.ID))
	require.NoError(t, err)
	assert.Equal(t, strings.Join(codes.Codes, "\n"), string(copied))
	assert.True(t, now.Add(15*time.Minute).Equal(waiting.ExpiresAt))
	require.NoError(t, totp.VerifyWaiting(waiting, codes.Token, now))
	status, err := service.Status(t.Context(), alice.ID)
	require.NoError(t, err)
	assert.Equal(t, totp.Status{State: totp.StatePending, BackupCodesWaiting: true}, status)

	// Showing the codes again keeps them and replaces the token.
	again, err := service.ShowWaiting(t.Context(), alice, "password", netip.MustParseAddr("192.0.2.1"))
	require.NoError(t, err)
	assert.Equal(t, codes.Codes, again.Codes)
	shown := readWaitingCodes(t, conn, alice.ID)
	require.ErrorIs(t, totp.VerifyWaiting(shown, codes.Token, now), totp.ErrNotWaiting)
	require.NoError(t, totp.VerifyWaiting(shown, again.Token, now))
	assert.Equal(t, waiting.Hashes, shown.Hashes)

	// Cancelling removes the waiting codes with the enrollment.
	require.NoError(t, service.Cancel(t.Context(), alice.ID))
	assert.Nil(t, readWaitingCodes(t, conn, alice.ID))
}

func TestTOTPWaitingCodesBelongToTheirUser(t *testing.T) {
	db, conn := setupStorage(t)
	alice := backupCodeUser(t, conn, "alice@example.com")
	bob := backupCodeUser(t, conn, "bob@example.com")
	now := time.Now().UTC()
	waiting := totp.Waiting{Hashes: []string{"hash"}, KeyID: "1", Ciphertext: []byte("copy"), ExpiresAt: now}
	require.NoError(t, db.UpdateTOTP(t.Context(), alice, func(totp.Record) (totp.Change, error) {
		return totp.Change{WaitingWrite: totp.SaveWaiting, Waiting: &waiting}, nil
	}))

	assert.Nil(t, readWaitingCodes(t, conn, bob))
	require.NoError(t, db.UpdateTOTP(t.Context(), bob, func(current totp.Record) (totp.Change, error) {
		assert.Nil(t, current.Waiting)
		return totp.Change{WaitingWrite: totp.DeleteWaiting}, nil
	}))
	assert.NotNil(t, readWaitingCodes(t, conn, alice), "deleting is per user")
}

func TestTOTPWaitingCopyWhole(t *testing.T) {
	db, conn := setupStorage(t)
	alice := backupCodeUser(t, conn, "alice@example.com")
	now := time.Now().UTC()

	// A display copy is its key and ciphertext together, or neither.
	half := totp.Waiting{Hashes: []string{"hash"}, KeyID: "1", ExpiresAt: now}
	err := db.UpdateTOTP(t.Context(), alice, func(totp.Record) (totp.Change, error) {
		return totp.Change{WaitingWrite: totp.SaveWaiting, Waiting: &half}, nil
	})
	require.Error(t, err)
	assert.Nil(t, readWaitingCodes(t, conn, alice))

	none := totp.Waiting{Hashes: []string{"hash"}, ExpiresAt: now}
	require.NoError(t, db.UpdateTOTP(t.Context(), alice, func(totp.Record) (totp.Change, error) {
		return totp.Change{WaitingWrite: totp.SaveWaiting, Waiting: &none}, nil
	}))
	saved := readWaitingCodes(t, conn, alice)
	require.NotNil(t, saved)
	assert.Empty(t, saved.KeyID)
	assert.Empty(t, saved.Ciphertext)
}

func TestTOTPEnrollmentSerializes(t *testing.T) {
	db, conn := setupStorage(t)
	var id int64
	require.NoError(t, conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ('alice@example.com', 'unused', 'member') RETURNING id").Scan(&id))
	require.Positive(t, id)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ready, release, secondStarted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() {
		firstDone <- db.UpdateTOTP(ctx, id, func(current totp.Record) (totp.Change, error) {
			if current.Factor != nil {
				return totp.Change{}, errors.New("expected no factor")
			}
			close(ready)
			select {
			case <-release:
				return totp.Change{Factor: &totp.Factor{KeyID: "1", Ciphertext: []byte("first ciphertext"), LastStep: -1}}, nil
			case <-ctx.Done():
				return totp.Change{}, ctx.Err()
			}
		})
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		require.FailNow(t, "first update did not acquire lock")
	}
	go func() {
		close(secondStarted)
		secondDone <- db.UpdateTOTP(ctx, id, func(current totp.Record) (totp.Change, error) {
			if current.Factor == nil || string(current.Factor.Ciphertext) != "first ciphertext" {
				return totp.Change{}, errors.New("did not see committed factor")
			}
			return totp.Change{Factor: &totp.Factor{KeyID: "1", Ciphertext: []byte("second ciphertext"), LastStep: -1}}, nil
		})
	}()
	<-secondStarted
	require.Eventually(t, func() bool {
		var blocked bool
		err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%FOR UPDATE%')").Scan(&blocked)
		return err == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	unblock()
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	final, err := db.ReadTOTP(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, final)
	assert.Equal(t, "second ciphertext", string(final.Ciphertext))
}

func TestTOTPUnknownUser(t *testing.T) {
	db, _ := setupStorage(t)
	err := db.UpdateTOTP(t.Context(), 123, func(totp.Record) (totp.Change, error) {
		t.Fatal("must not call update for an unknown user")
		return totp.Change{}, nil
	})
	require.ErrorIs(t, err, account.ErrUserNotFound)
}

func TestTOTPMigrationDown(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	_, err := provider.DownTo(t.Context(), 4)
	require.NoError(t, err)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	// The waiting codes' migration goes down and up again, on an empty
	// schema: nothing is deployed, so no saved rows need carrying over.
	_, err = provider.DownTo(t.Context(), 31)
	require.NoError(t, err)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
}

func TestTOTPCommitFailure(t *testing.T) {
	db, conn := setupStorage(t)
	var id int64
	require.NoError(t, conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ('alice@example.com', 'unused', 'member') RETURNING id").Scan(&id))
	require.Positive(t, id)
	_, err := conn.Exec(t.Context(), `
  CREATE FUNCTION reject_totp() RETURNS trigger LANGUAGE plpgsql AS
  $$ BEGIN RAISE EXCEPTION 'forced TOTP failure'; END $$;
  CREATE CONSTRAINT TRIGGER reject_totp AFTER INSERT ON user_totp
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_totp();`)
	require.NoError(t, err)
	service := newTOTPService(t, db, time.Now)
	result, err := service.Start(t.Context(), account.User{ID: id, Email: "alice@example.com"}, "password", netip.MustParseAddr("192.0.2.1"))
	require.ErrorContains(t, err, "forced TOTP failure")
	assert.Empty(t, result)
	factor, err := db.ReadTOTP(t.Context(), id)
	require.NoError(t, err)
	assert.Nil(t, factor)
}

func TestTOTPStatusBackupCodes(t *testing.T) {
	db, conn := setupStorage(t)
	alice := backupCodeUser(t, conn, "alice@example.com")
	bob := backupCodeUser(t, conn, "bob@example.com")
	service := newTOTPService(t, db, time.Now)
	queries := sqlc.New(conn)

	// Count rows, not hash contents; another user's codes must stay separate.
	_, err := conn.Exec(t.Context(), `INSERT INTO totp_backup_codes (user_id, code_hash)
 SELECT $1, 'unused-hash' FROM generate_series(1, 10)`, alice)
	require.NoError(t, err)
	require.NoError(t, queries.InsertTOTPBackupCode(t.Context(), sqlc.InsertTOTPBackupCodeParams{UserID: bob, CodeHash: "other-hash"}))

	status, err := service.Status(t.Context(), alice)
	require.NoError(t, err)
	assert.Equal(t, totp.Status{State: totp.StateOn, BackupCodesLeft: 10}, status)
	other, err := service.Status(t.Context(), bob)
	require.NoError(t, err)
	assert.Equal(t, totp.Status{State: totp.StateOn, BackupCodesLeft: 1}, other)

	// Committed consumption reduces the count without changing factor status.
	codes, err := queries.ReadTOTPBackupCodes(t.Context(), alice)
	require.NoError(t, err)
	require.Len(t, codes, 10)
	_, err = queries.ConsumeTOTPBackupCode(t.Context(), sqlc.ConsumeTOTPBackupCodeParams{UserID: alice, ID: codes[0].ID})
	require.NoError(t, err)
	status, err = service.Status(t.Context(), alice)
	require.NoError(t, err)
	assert.Equal(t, totp.Status{State: totp.StateOn, BackupCodesLeft: 9}, status)

	require.NoError(t, queries.DeleteTOTPBackupCodes(t.Context(), alice))
	status, err = service.Status(t.Context(), alice)
	require.NoError(t, err)
	assert.Equal(t, totp.Status{State: totp.StateOn}, status)

	// Pending and missing factors report no usable backup codes.
	_, err = conn.Exec(t.Context(), "UPDATE user_totp SET confirmed_at = NULL WHERE user_id = $1", bob)
	require.NoError(t, err)
	status, err = service.Status(t.Context(), bob)
	require.NoError(t, err)
	assert.Equal(t, totp.Status{State: totp.StatePending}, status)
	_, err = conn.Exec(t.Context(), "DELETE FROM user_totp WHERE user_id = $1", bob)
	require.NoError(t, err)
	status, err = service.Status(t.Context(), bob)
	require.NoError(t, err)
	assert.Equal(t, totp.Status{State: totp.StateOff}, status)
}
