package totp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type backupStorage struct {
	factor             *Factor
	change             *BackupChange
	readErr, commitErr error
	calls              int
	userID             int64
}

func (f *backupStorage) ReplaceTOTPBackupCodes(_ context.Context, userID int64, update func(*Factor) (BackupChange, error)) error {
	f.calls++
	f.userID = userID
	if f.readErr != nil {
		return f.readErr
	}
	next, err := update(f.factor)
	if err != nil {
		return err
	}
	if f.commitErr != nil {
		return f.commitErr
	}
	f.change = &next
	return nil
}

type backupOpener struct {
	err   error
	owner string
	calls int
}

func (f *backupOpener) Open(_ string, _ []byte, owner string) ([]byte, error) {
	f.calls++
	f.owner = owner
	return []byte(vectorSecret), f.err
}

type backupAttempts struct {
	calls    int
	err      error
	released []int64
}

func (f *backupAttempts) Begin(context.Context, int64) (int64, error) {
	f.calls++
	return 7, f.err
}

func (f *backupAttempts) Release(_ context.Context, id int64) {
	f.released = append(f.released, id)
}

func backupFixture() (*Backups, *backupStorage, *backupOpener, *backupAttempts) {
	now := time.Unix(59, 0).UTC()
	store := &backupStorage{factor: &Factor{KeyID: "1", Ciphertext: []byte("encrypted"), ConfirmedAt: &now, LastStep: -1}}
	opener, attempts := &backupOpener{}, &backupAttempts{}
	return NewBackups(store, opener, NewVerifier(func() time.Time { return now }), attempts), store, opener, attempts
}

func TestRegenerateBackupCodes(t *testing.T) {
	service, store, opener, attempts := backupFixture()

	codes, err := service.Regenerate(t.Context(), 42, "287082")

	require.NoError(t, err)
	require.NotNil(t, store.change)
	require.Len(t, codes, 10)
	require.Len(t, store.change.Hashes, 10)
	for i, code := range codes {
		require.NoError(t, VerifyBackupCode(t.Context(), code, store.change.Hashes[i]))
	}
	assert.Equal(t, int64(42), store.userID)
	assert.Equal(t, "totp:42", opener.owner)
	assert.Equal(t, int64(1), store.change.Factor.LastStep)
	assert.Equal(t, int64(-1), store.factor.LastStep, "do not mutate the loaded factor before commit")
	assert.Equal(t, store.factor.ConfirmedAt, store.change.Factor.ConfirmedAt)
	assert.Equal(t, store.factor.Ciphertext, store.change.Factor.Ciphertext)
	assert.Equal(t, int64(7), store.change.AttemptID)
	assert.Equal(t, 1, attempts.calls)
}

func TestRegenerateRejected(t *testing.T) {
	// wantRelease means the request checked no code, so it releases its attempt.
	for _, c := range []struct {
		name        string
		edit        func(*backupStorage)
		code        string
		want        error
		wantRelease bool
	}{
		{"missing", func(s *backupStorage) { s.factor = nil }, "287082", ErrNotOn, true},
		{"pending", func(s *backupStorage) { s.factor.ConfirmedAt = nil }, "287082", ErrNotOn, true},
		{"wrong", func(*backupStorage) {}, "000000", ErrInvalidCode, false},
		{"backup proof", func(*backupStorage) {}, "ABCDE-FGHJK", ErrInvalidCode, false},
		{"replay", func(s *backupStorage) { s.factor.LastStep = 1 }, "287082", ErrInvalidCode, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, _, attempts := backupFixture()
			c.edit(store)

			codes, err := service.Regenerate(t.Context(), 42, c.code)

			require.ErrorIs(t, err, c.want)
			assert.Nil(t, codes)
			assert.Nil(t, store.change)
			assert.Equal(t, 1, attempts.calls)
			if c.wantRelease {
				assert.Equal(t, []int64{7}, attempts.released)
			} else {
				assert.Empty(t, attempts.released)
			}
		})
	}
}

func TestRegenerateErrors(t *testing.T) {
	lost := errors.New("storage or encryption failure")
	for _, stage := range []string{"throttle", "read", "open", "commit"} {
		t.Run(stage, func(t *testing.T) {
			service, store, opener, attempts := backupFixture()
			switch stage {
			case "throttle":
				attempts.err = ErrThrottled
			case "read":
				store.readErr = lost
			case "open":
				opener.err = lost
			case "commit":
				store.commitErr = lost
			}

			codes, err := service.Regenerate(t.Context(), 42, "287082")

			if stage == "throttle" {
				require.ErrorIs(t, err, ErrThrottled)
				assert.Zero(t, store.calls)
				assert.Zero(t, opener.calls)
			} else {
				require.ErrorIs(t, err, lost)
			}
			assert.Nil(t, codes, "never expose replacement codes on failure")
			assert.Nil(t, store.change)
			// Only a failure before any code check gives the attempt back.
			if stage == "read" || stage == "open" {
				assert.Equal(t, []int64{7}, attempts.released)
			} else {
				assert.Empty(t, attempts.released)
			}
		})
	}
}
