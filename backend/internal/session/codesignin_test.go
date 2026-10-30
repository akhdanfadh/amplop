package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/passwordhash"
	"inbok/internal/totp"
)

type codeSignInStorage struct {
	state               CodeSignInState
	change              *CodeSignInChange
	lookupErr, writeErr error
	calls               int
}

func (f *codeSignInStorage) ChallengeUser(context.Context, [sha256.Size]byte) (int64, error) {
	return 42, f.lookupErr
}

func (f *codeSignInStorage) CompleteSignIn(_ context.Context, _ int64, _ [sha256.Size]byte, update func(CodeSignInState) (CodeSignInChange, error)) error {
	f.calls++
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

	issued, err := service.Complete(t.Context(), secret, "287082")

	require.NoError(t, err)
	require.NotNil(t, store.change)
	assert.Equal(t, CodeActionComplete, store.change.Action)
	assert.Equal(t, issued.State, store.change.Session)
	require.NotNil(t, store.change.Factor)
	assert.Equal(t, int64(1), store.change.Factor.LastStep)
	assert.Equal(t, int64(-1), store.state.Factor.LastStep, "do not mutate the loaded factor before commit")
	assert.Zero(t, store.change.BackupCodeID)
	assert.Equal(t, int64(7), store.change.AttemptID)
	assert.Equal(t, int64(42), attempts.userID)
	hash, err := hashSecret(issued.Secret)
	require.NoError(t, err)
	assert.Equal(t, hash, issued.State.TokenHash)
}

func TestCodeSignInBackup(t *testing.T) {
	service, store, _, secret := codeSignInFixture()
	store.state.BackupCodes = []BackupCode{{ID: 9, Hash: passwordhash.Hash("ZZZZZZZZZZ")}, {ID: 10, Hash: passwordhash.Hash("ABCDEFGHJK")}}

	issued, err := service.Complete(t.Context(), secret, "abcde-fghjk")

	require.NoError(t, err)
	assert.NotEmpty(t, issued.Secret)
	require.NotNil(t, store.change)
	assert.Equal(t, int64(10), store.change.BackupCodeID)
	assert.Nil(t, store.change.Factor, "a backup code does not consume a TOTP step")
}

func TestCodeSignInRejected(t *testing.T) {
	for _, c := range []struct {
		name   string
		edit   func(*CodeSignInState)
		code   string
		want   error
		action CodeAction
	}{
		{"wrong", func(*CodeSignInState) {}, "000000", totp.ErrInvalidCode, CodeActionFail},
		{"malformed", func(*CodeSignInState) {}, "abc", totp.ErrInvalidCode, CodeActionFail},
		{"replay", func(s *CodeSignInState) { s.Factor.LastStep = 1 }, "287082", totp.ErrInvalidCode, CodeActionFail},
		{"fifth wrong", func(s *CodeSignInState) { s.Challenge.Failures = 4 }, "000000", ErrInvalidChallenge, CodeActionDelete},
		{"expired", func(s *CodeSignInState) { s.Challenge.ExpiresAt = time.Unix(59, 0).UTC() }, "287082", ErrInvalidChallenge, CodeActionDelete},
		{"missing factor", func(s *CodeSignInState) { s.Factor = nil }, "287082", ErrInvalidChallenge, CodeActionDelete},
		{"pending factor", func(s *CodeSignInState) { s.Factor.ConfirmedAt = nil }, "287082", ErrInvalidChallenge, CodeActionDelete},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, _, secret := codeSignInFixture()
			c.edit(&store.state)

			issued, err := service.Complete(t.Context(), secret, c.code)

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, issued)
			require.NotNil(t, store.change, "expected failures must commit their counter or deletion")
			assert.Equal(t, c.action, store.change.Action)
			if c.action == CodeActionFail {
				assert.Equal(t, store.state.Challenge.Failures+1, store.change.Failures)
			}
		})
	}
}

func TestCodeSignInErrors(t *testing.T) {
	lost := errors.New("commit failed")
	for _, c := range []struct {
		name                    string
		lookup, throttle, write error
		want                    error
	}{
		{"missing", ErrNotFound, nil, nil, ErrInvalidChallenge},
		{"lookup", lost, nil, nil, lost},
		{"throttled", nil, totp.ErrThrottled, nil, totp.ErrThrottled},
		{"commit", nil, nil, lost, lost},
		{"consumed", nil, nil, ErrNotFound, ErrInvalidChallenge},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, attempts, secret := codeSignInFixture()
			store.lookupErr, store.writeErr, attempts.err = c.lookup, c.write, c.throttle

			issued, err := service.Complete(t.Context(), secret, "287082")

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, issued)
			assert.Nil(t, store.change)
			if c.lookup != nil || c.throttle != nil {
				assert.Zero(t, store.calls)
			}
		})
	}
	service, store, attempts, _ := codeSignInFixture()
	_, err := service.Complete(t.Context(), "malformed cookie", "287082")
	require.ErrorIs(t, err, ErrInvalidChallenge)
	assert.Zero(t, attempts.calls)
	assert.Zero(t, store.calls)
}
