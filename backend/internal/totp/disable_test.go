package totp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/passwordhash"
)

type disableStorage struct {
	state             DisableState
	commitErr         error
	disabled          bool
	calls             int
	userID, attemptID int64
}

func (f *disableStorage) DisableTOTP(_ context.Context, userID, attemptID int64, check func(DisableState) error) error {
	f.calls++
	f.userID, f.attemptID = userID, attemptID
	if err := check(f.state); err != nil {
		return err
	}
	if f.commitErr != nil {
		return f.commitErr
	}
	f.disabled = true
	return nil
}

func disableFixture() (*Disabler, *disableStorage, *backupOpener, *backupAttempts) {
	now := time.Unix(59, 0).UTC()
	store := &disableStorage{state: DisableState{
		Role:   account.RoleMember,
		Factor: &Factor{KeyID: "1", Ciphertext: []byte("encrypted"), ConfirmedAt: &now, LastStep: -1},
	}}
	opener, attempts := &backupOpener{}, &backupAttempts{}
	return NewDisabler(store, opener, NewVerifier(func() time.Time { return now }), attempts), store, opener, attempts
}

func TestDisableProof(t *testing.T) {
	for _, kind := range []string{"totp", "backup"} {
		t.Run(kind, func(t *testing.T) {
			service, store, opener, attempts := disableFixture()
			code := "287082"
			if kind == "backup" {
				code = "abcde-fghjk"
				store.state.BackupHashes = []string{passwordhash.Hash("ZZZZZZZZZZ"), passwordhash.Hash("ABCDEFGHJK")}
				opener.err = errors.New("old encryption key unavailable")
			}

			err := service.Disable(t.Context(), 42, code)

			require.NoError(t, err)
			assert.True(t, store.disabled)
			assert.Empty(t, attempts.released, "storage removes a successful attempt")
			assert.Equal(t, int64(42), store.userID)
			assert.Equal(t, int64(7), store.attemptID)
			assert.Equal(t, 1, attempts.calls)
			if kind == "backup" {
				assert.Zero(t, opener.calls)
			} else {
				assert.Equal(t, "totp:42", opener.owner)
			}
		})
	}
}

func TestDisableRejected(t *testing.T) {
	// wantRelease means the request checked no code, so it releases its attempt.
	for _, c := range []struct {
		name        string
		edit        func(*disableStorage)
		code        string
		want        error
		wantRelease bool
	}{
		{"admin", func(s *disableStorage) { s.state.Role = account.RoleAdmin }, "287082", ErrDisableForbidden, true},
		{"unknown role", func(s *disableStorage) { s.state.Role = "unknown" }, "287082", ErrDisableForbidden, true},
		{"missing", func(s *disableStorage) { s.state.Factor = nil }, "287082", ErrNotOn, true},
		{"pending", func(s *disableStorage) { s.state.Factor.ConfirmedAt = nil }, "287082", ErrNotOn, true},
		{"wrong", func(*disableStorage) {}, "000000", ErrInvalidCode, false},
		{"malformed", func(*disableStorage) {}, "abc", ErrInvalidCode, false},
		{"replay", func(s *disableStorage) { s.state.Factor.LastStep = 1 }, "287082", ErrInvalidCode, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, _, attempts := disableFixture()
			c.edit(store)

			err := service.Disable(t.Context(), 42, c.code)

			require.ErrorIs(t, err, c.want)
			assert.False(t, store.disabled)
			assert.Equal(t, 1, attempts.calls)
			if c.wantRelease {
				assert.Equal(t, []int64{7}, attempts.released)
			} else {
				assert.Empty(t, attempts.released)
			}
		})
	}
}

func TestDisableErrors(t *testing.T) {
	lost := errors.New("storage or encryption failure")
	for _, stage := range []string{"throttle", "open", "commit", "stored backup"} {
		t.Run(stage, func(t *testing.T) {
			service, store, opener, attempts := disableFixture()
			code := "287082"
			want := lost
			switch stage {
			case "throttle":
				attempts.err = ErrThrottled
				want = ErrThrottled
			case "open":
				opener.err = lost
			case "commit":
				store.commitErr = lost
			case "stored backup":
				store.state.BackupHashes = []string{"malformed PHC hash"}
				code = "ABCDE-FGHJK"
				want = errBackupHash
			}

			err := service.Disable(t.Context(), 42, code)

			require.ErrorIs(t, err, want)
			assert.False(t, store.disabled)
			if stage == "throttle" {
				assert.Zero(t, store.calls)
				assert.Zero(t, opener.calls)
			}
			// Only a failure before any code check gives the attempt back.
			if stage == "open" {
				assert.Equal(t, []int64{7}, attempts.released)
			} else {
				assert.Empty(t, attempts.released)
			}
		})
	}
}
