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
	"inbok/internal/randomsecret"
	"inbok/internal/totp"
)

// RFC 6238's SHA-1 test secret; code 287082 matches step 1 at Unix second 59.
const enrollmentSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

type enrollmentStorage struct {
	state              EnrollmentState
	change             *EnrollmentChange
	readErr, commitErr error
	calls              int
	userID             int64
	hash               [sha256.Size]byte
}

func (f *enrollmentStorage) ConfirmTOTP(_ context.Context, userID int64, hash [sha256.Size]byte, update func(EnrollmentState) (EnrollmentChange, error)) error {
	f.calls++
	f.userID, f.hash = userID, hash
	if f.readErr != nil {
		return f.readErr
	}
	change, err := update(f.state)
	if err != nil {
		return err
	}
	if f.commitErr != nil {
		return f.commitErr
	}
	f.change = &change
	return nil
}

type enrollmentOpener struct {
	keyID string
	owner string
	data  []byte
	err   error
	calls int
}

func (f *enrollmentOpener) Open(keyID string, data []byte, owner string) ([]byte, error) {
	f.calls++
	f.keyID, f.data, f.owner = keyID, data, owner
	return []byte(enrollmentSecret), f.err
}

type enrollmentAttempts struct {
	calls    int
	userID   int64
	err      error
	released []int64
}

func (f *enrollmentAttempts) Begin(_ context.Context, userID int64) (int64, error) {
	f.calls++
	f.userID = userID
	return 7, f.err
}

func (f *enrollmentAttempts) Release(_ context.Context, id int64) {
	f.released = append(f.released, id)
}

func enrollmentFixture() (*Enrollment, *enrollmentStorage, *enrollmentOpener, *enrollmentAttempts, Authenticated, *fakeClock) {
	clock := &fakeClock{now: time.Unix(59, 0).UTC()}
	issued := newSession(42, Device{}, clock.Now)
	store := &enrollmentStorage{state: EnrollmentState{
		Session: issued.State,
		Factor:  &totp.Factor{KeyID: "2", Ciphertext: []byte("encrypted"), LastStep: -1},
	}}
	opener, attempts := &enrollmentOpener{}, &enrollmentAttempts{}
	current := Authenticated{Identity: Identity{User: account.User{ID: 42}}, Session: Reference{hash: issued.State.TokenHash}}
	return NewEnrollment(store, opener, totp.NewVerifier(clock.Now), attempts, clock.Now), store, opener, attempts, current, clock
}

func TestConfirmEnrollment(t *testing.T) {
	service, store, opener, attempts, current, clock := enrollmentFixture()

	device := Device{UserAgent: "confirming browser", Addr: netip.MustParseAddr("198.51.100.4")}
	result, err := service.Confirm(t.Context(), current, "287082", device)
	require.NoError(t, err)

	require.NotNil(t, store.change)
	assert.Equal(t, device, store.change.Session.Device)
	assert.Equal(t, current.Identity.User.ID, attempts.userID)
	assert.Equal(t, current.Identity.User.ID, store.userID)
	assert.Equal(t, current.Session.hash, store.hash)
	assert.Equal(t, "totp:42", opener.owner)
	assert.Equal(t, store.state.Factor.KeyID, opener.keyID)
	assert.Equal(t, store.state.Factor.Ciphertext, opener.data)
	assert.Nil(t, store.state.Factor.ConfirmedAt, "the callback must not mutate stored input")
	assert.Equal(t, int64(1), store.change.Factor.LastStep)
	require.NotNil(t, store.change.Factor.ConfirmedAt)
	assert.Equal(t, clock.Now(), *store.change.Factor.ConfirmedAt)
	assert.Equal(t, int64(7), store.change.AttemptID)
	assert.Equal(t, current.Identity.User.ID, result.Session.State.UserID)
	assert.Equal(t, clock.Now(), result.Session.State.CreatedAt)
	assert.NotEqual(t, current.Session.hash, result.Session.State.TokenHash)
	hash, err := randomsecret.Hash(result.Session.Secret)
	require.NoError(t, err)
	assert.Equal(t, result.Session.State.TokenHash, hash)
	assert.Equal(t, result.Session.State, store.change.Session)
	require.Len(t, result.BackupCodes, 10)
	require.Len(t, store.change.BackupHashes, 10)
	for i, code := range result.BackupCodes {
		require.NoError(t, totp.VerifyBackupCode(t.Context(), code, store.change.BackupHashes[i]))
	}
}

func TestConfirmEnrollmentRejected(t *testing.T) {
	failure := errors.New("storage unavailable")
	// wantRelease means the request checked no code, so it releases its attempt.
	for _, c := range []struct {
		name        string
		edit        func(*enrollmentStorage, *enrollmentOpener, *fakeClock)
		code        string
		want        error
		wantRelease bool
	}{
		{"missing factor", func(s *enrollmentStorage, _ *enrollmentOpener, _ *fakeClock) { s.state.Factor = nil }, "287082", totp.ErrNotPending, true},
		{"already on", func(s *enrollmentStorage, _ *enrollmentOpener, c *fakeClock) {
			now := c.Now()
			s.state.Factor.ConfirmedAt = &now
		}, "287082", totp.ErrAlreadyOn, true},
		{"replay", func(s *enrollmentStorage, _ *enrollmentOpener, _ *fakeClock) { s.state.Factor.LastStep = 1 }, "287082", totp.ErrInvalidCode, false},
		{"expired session", func(s *enrollmentStorage, _ *enrollmentOpener, c *fakeClock) { s.state.Session.ExpiresAt = c.Now() }, "287082", ErrUnauthenticated, true},
		{"wrong owner", func(s *enrollmentStorage, _ *enrollmentOpener, _ *fakeClock) { s.state.Session.UserID++ }, "287082", ErrUnauthenticated, true},
		{"wrong session", func(s *enrollmentStorage, _ *enrollmentOpener, _ *fakeClock) { s.state.Session.TokenHash[0]++ }, "287082", ErrUnauthenticated, true},
		{"missing session", func(s *enrollmentStorage, _ *enrollmentOpener, _ *fakeClock) { s.readErr = ErrNotFound }, "287082", ErrUnauthenticated, true},
		{"storage", func(s *enrollmentStorage, _ *enrollmentOpener, _ *fakeClock) { s.readErr = failure }, "287082", failure, true},
		{"commit", func(s *enrollmentStorage, _ *enrollmentOpener, _ *fakeClock) { s.commitErr = failure }, "287082", failure, false},
		{"decryption", func(_ *enrollmentStorage, o *enrollmentOpener, _ *fakeClock) { o.err = failure }, "287082", failure, true},
		{"wrong code", func(*enrollmentStorage, *enrollmentOpener, *fakeClock) {}, "000000", totp.ErrInvalidCode, false},
		{"malformed code", func(*enrollmentStorage, *enrollmentOpener, *fakeClock) {}, "invalid", totp.ErrInvalidCode, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, opener, attempts, current, clock := enrollmentFixture()
			c.edit(store, opener, clock)

			result, err := service.Confirm(t.Context(), current, c.code, Device{})

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, result, "failed confirmation must reveal no session or backup codes")
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

func TestConfirmEnrollmentBlocked(t *testing.T) {
	service, store, opener, attempts, current, _ := enrollmentFixture()
	attempts.err = totp.ErrThrottled

	result, err := service.Confirm(t.Context(), current, "287082", Device{})

	require.ErrorIs(t, err, totp.ErrThrottled)
	assert.Empty(t, result)
	assert.Zero(t, store.calls)
	assert.Zero(t, opener.calls)
	assert.Empty(t, attempts.released, "a refused reservation saved nothing to release")
}

func TestConfirmEnrollmentUnchecked(t *testing.T) {
	for _, current := range []Authenticated{
		{},
		{Identity: Identity{User: account.User{ID: 42}}},
		{Session: Reference{hash: sha256.Sum256([]byte("unchecked"))}},
	} {
		service, store, opener, attempts, _, _ := enrollmentFixture()

		result, err := service.Confirm(t.Context(), current, "287082", Device{})

		require.ErrorIs(t, err, ErrUnauthenticated)
		assert.Empty(t, result)
		assert.Zero(t, attempts.calls)
		assert.Zero(t, store.calls)
		assert.Zero(t, opener.calls)
	}
}
