package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/passwordhash"
	"inbok/internal/randomsecret"
	"inbok/internal/totp"
)

type codeSignInStorage struct {
	state                        CodeSignInState
	change                       *CodeSignInChange
	lookupErr, readErr, writeErr error
	calls                        int
}

func (f *codeSignInStorage) ChallengeUser(context.Context, [sha256.Size]byte) (int64, error) {
	return 42, f.lookupErr
}

func (f *codeSignInStorage) CompleteSignIn(_ context.Context, _ int64, _ [sha256.Size]byte, update func(CodeSignInState) (CodeSignInChange, error)) error {
	f.calls++
	if f.readErr != nil {
		return f.readErr
	}
	next, err := update(f.state)
	if err != nil {
		return err
	}
	if f.writeErr != nil {
		return f.writeErr
	}
	f.change = &next
	return nil
}

func codeSignInFixture() (*CodeSignIn, *codeSignInStorage, *enrollmentAttempts, string) {
	clock := &fakeClock{now: time.Unix(59, 0).UTC()}
	secret, challenge := newChallenge(42, clock.Now)
	store := &codeSignInStorage{state: CodeSignInState{
		Challenge: challenge,
		Factor:    &totp.Factor{KeyID: "1", Ciphertext: []byte("encrypted"), ConfirmedAt: &clock.now, LastStep: -1},
	}}
	attempts := &enrollmentAttempts{}
	return NewCodeSignIn(store, &enrollmentOpener{}, totp.NewVerifier(clock.Now), attempts, clock.Now), store, attempts, secret
}

func TestCodeSignInTOTP(t *testing.T) {
	service, store, attempts, secret := codeSignInFixture()

	device := Device{UserAgent: "code request", Addr: netip.MustParseAddr("2001:db8::7")}
	issued, err := service.Complete(t.Context(), secret, "287082", device)

	require.NoError(t, err)
	require.NotNil(t, store.change)
	assert.Equal(t, CodeActionComplete, store.change.Action)
	assert.Equal(t, issued.State, store.change.Session)
	assert.Equal(t, device, store.change.Session.Device)
	now := issued.State.CreatedAt
	assert.Equal(t, Expiry{ExpiresBy: now, LastUsedBy: now.Add(-idleTimeout)}, store.change.Expiry,
		"the new session clears the user's expired ones")
	require.NotNil(t, store.change.Factor)
	assert.Equal(t, int64(1), store.change.Factor.LastStep)
	assert.Equal(t, int64(-1), store.state.Factor.LastStep, "do not mutate the loaded factor before commit")
	assert.Zero(t, store.change.BackupCodeID)
	assert.Equal(t, int64(7), store.change.AttemptID)
	assert.Equal(t, int64(42), attempts.userID)
	hash, err := randomsecret.Hash(issued.Secret)
	require.NoError(t, err)
	assert.Equal(t, hash, issued.State.TokenHash)
}

func TestCodeSignInBackup(t *testing.T) {
	service, store, _, secret := codeSignInFixture()
	store.state.BackupCodes = []BackupCode{{ID: 9, Hash: passwordhash.Hash("ZZZZZZZZZZ")}, {ID: 10, Hash: passwordhash.Hash("ABCDEFGHJK")}}

	issued, err := service.Complete(t.Context(), secret, "abcde-fghjk", Device{})

	require.NoError(t, err)
	assert.NotEmpty(t, issued.Secret)
	require.NotNil(t, store.change)
	assert.Equal(t, int64(10), store.change.BackupCodeID)
	assert.Nil(t, store.change.Factor, "a backup code does not consume a TOTP step")
}

func TestCodeSignInRejected(t *testing.T) {
	// wantRelease means the request checked no code, so it releases its attempt.
	for _, c := range []struct {
		name        string
		edit        func(*CodeSignInState)
		code        string
		want        error
		action      CodeAction
		wantRelease bool
	}{
		{"wrong", func(*CodeSignInState) {}, "000000", totp.ErrInvalidCode, CodeActionFail, false},
		{"malformed", func(*CodeSignInState) {}, "abc", totp.ErrInvalidCode, CodeActionFail, false},
		{"replay", func(s *CodeSignInState) { s.Factor.LastStep = 1 }, "287082", totp.ErrInvalidCode, CodeActionFail, false},
		{"fifth wrong", func(s *CodeSignInState) { s.Challenge.Failures = 4 }, "000000", ErrInvalidChallenge, CodeActionDelete, false},
		{"expired", func(s *CodeSignInState) { s.Challenge.ExpiresAt = time.Unix(59, 0).UTC() }, "287082", ErrInvalidChallenge, CodeActionDelete, true},
		{"missing factor", func(s *CodeSignInState) { s.Factor = nil }, "287082", ErrInvalidChallenge, CodeActionDelete, true},
		{"pending factor", func(s *CodeSignInState) { s.Factor.ConfirmedAt = nil }, "287082", ErrInvalidChallenge, CodeActionDelete, true},
		{"deactivated", func(s *CodeSignInState) { s.Deactivated = true }, "287082", account.ErrDeactivated, CodeActionDelete, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, attempts, secret := codeSignInFixture()
			c.edit(&store.state)

			issued, err := service.Complete(t.Context(), secret, c.code, Device{})

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, issued)
			require.NotNil(t, store.change, "expected failures must commit their counter or deletion")
			assert.Equal(t, c.action, store.change.Action)
			if c.action == CodeActionFail {
				assert.Equal(t, store.state.Challenge.Failures+1, store.change.Failures)
			}
			if c.wantRelease {
				assert.Equal(t, []int64{7}, attempts.released)
			} else {
				assert.Empty(t, attempts.released)
			}
		})
	}
}

func TestCodeSignInErrors(t *testing.T) {
	lost := errors.New("commit failed")
	// read fails before the code check, so the request releases its
	// attempt; write fails after it, so the attempt counts.
	for _, c := range []struct {
		name                          string
		lookup, throttle, read, write error
		want                          error
		wantRelease                   bool
	}{
		{"missing", ErrNotFound, nil, nil, nil, ErrInvalidChallenge, false},
		{"lookup", lost, nil, nil, nil, lost, false},
		{"throttled", nil, totp.ErrThrottled, nil, nil, totp.ErrThrottled, false},
		{"consumed", nil, nil, ErrNotFound, nil, ErrInvalidChallenge, true},
		{"read", nil, nil, lost, nil, lost, true},
		{"commit", nil, nil, nil, lost, lost, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, attempts, secret := codeSignInFixture()
			store.lookupErr, store.readErr, store.writeErr, attempts.err = c.lookup, c.read, c.write, c.throttle

			issued, err := service.Complete(t.Context(), secret, "287082", Device{})

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, issued)
			assert.Nil(t, store.change)
			if c.lookup != nil || c.throttle != nil {
				assert.Zero(t, store.calls)
			}
			if c.wantRelease {
				assert.Equal(t, []int64{7}, attempts.released)
			} else {
				assert.Empty(t, attempts.released)
			}
		})
	}
	service, store, attempts, _ := codeSignInFixture()
	_, err := service.Complete(t.Context(), "malformed cookie", "287082", Device{})
	require.ErrorIs(t, err, ErrInvalidChallenge)
	assert.Zero(t, attempts.calls)
	assert.Zero(t, store.calls)
}

// A challenge that no longer matches, or a secret that cannot be decrypted,
// stops the request before any code check, so it releases its attempt.
func TestCodeSignInUnchecked(t *testing.T) {
	failure := errors.New("key unavailable")
	for _, c := range []struct {
		name string
		edit func(*codeSignInStorage, *enrollmentOpener)
		want error
	}{
		{"other user's challenge", func(s *codeSignInStorage, _ *enrollmentOpener) { s.state.Challenge.UserID++ }, ErrInvalidChallenge},
		{"decryption", func(_ *codeSignInStorage, o *enrollmentOpener) { o.err = failure }, failure},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, attempts, secret := codeSignInFixture()
			c.edit(store, service.encryption.(*enrollmentOpener))

			issued, err := service.Complete(t.Context(), secret, "287082", Device{})

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, issued)
			assert.Nil(t, store.change)
			assert.Equal(t, []int64{7}, attempts.released)
		})
	}
}
