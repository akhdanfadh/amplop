package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/encryption"
	"inbok/internal/totp"
)

func TestTOTPEnrollmentStorage(t *testing.T) {
	db, conn := setupStorage(t)
	var id int64
	require.NoError(t, conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ('alice@example.com', 'unused', 'member') RETURNING id").Scan(&id))
	require.Positive(t, id)
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	service := totp.NewService(db, ring)
	status, err := service.Status(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, totp.StateOff, status.State)

	first, err := service.Start(t.Context(), account.User{ID: id, Email: "alice@example.com"})
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
	status, err = service.Status(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, totp.StatePending, status.State)

	second, err := service.Start(t.Context(), account.User{ID: id, Email: "alice@example.com"})
	require.NoError(t, err)
	assert.NotEqual(t, first.Secret, second.Secret)
	factor, err = db.ReadTOTP(t.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, factor)
	opened, err = ring.Open(factor.KeyID, factor.Ciphertext, "totp:"+fmt.Sprint(id))
	require.NoError(t, err)
	assert.Equal(t, second.Secret, string(opened))

	// A callback error rolls back a replacement.
	rejected := errors.New("replacement rejected")
	err = db.UpdateTOTP(t.Context(), id, func(*totp.Factor) (totp.Factor, error) { return totp.Factor{}, rejected })
	require.ErrorIs(t, err, rejected)
	unchanged, err := db.ReadTOTP(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, factor, unchanged)

	// The storage schema supports confirmation, but no HTTP operation activates it yet.
	_, err = conn.Exec(t.Context(), "UPDATE user_totp SET confirmed_at = now(), last_step = 12 WHERE user_id = $1", id)
	require.NoError(t, err)
	result, err := service.Start(t.Context(), account.User{ID: id, Email: "alice@example.com"})
	require.ErrorIs(t, err, totp.ErrAlreadyOn)
	assert.Empty(t, result)
	factor, err = db.ReadTOTP(t.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, factor)
	assert.NotNil(t, factor.ConfirmedAt)
	assert.Equal(t, int64(12), factor.LastStep)
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
		firstDone <- db.UpdateTOTP(ctx, id, func(current *totp.Factor) (totp.Factor, error) {
			if current != nil {
				return totp.Factor{}, errors.New("expected no factor")
			}
			close(ready)
			select {
			case <-release:
				return totp.Factor{KeyID: "1", Ciphertext: []byte("first ciphertext"), LastStep: -1}, nil
			case <-ctx.Done():
				return totp.Factor{}, ctx.Err()
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
		secondDone <- db.UpdateTOTP(ctx, id, func(current *totp.Factor) (totp.Factor, error) {
			if current == nil || string(current.Ciphertext) != "first ciphertext" {
				return totp.Factor{}, errors.New("did not see committed factor")
			}
			return totp.Factor{KeyID: "1", Ciphertext: []byte("second ciphertext"), LastStep: -1}, nil
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
	err := db.UpdateTOTP(t.Context(), 123, func(*totp.Factor) (totp.Factor, error) {
		t.Fatal("must not call update for an unknown user")
		return totp.Factor{}, nil
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
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	service := totp.NewService(db, ring)
	result, err := service.Start(t.Context(), account.User{ID: id, Email: "alice@example.com"})
	require.ErrorContains(t, err, "forced TOTP failure")
	assert.Empty(t, result)
	factor, err := db.ReadTOTP(t.Context(), id)
	require.NoError(t, err)
	assert.Nil(t, factor)
}
