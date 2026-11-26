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

	"amplop/internal/account"
	"amplop/internal/randomsecret"
	"amplop/internal/totp"
)

type enrollmentStorage struct {
	state              EnrollmentState
	change             *EnrollmentChange
	readErr, commitErr error
	calls              int
	userID             int64
	hash               [sha256.Size]byte
}

func (f *enrollmentStorage) FinishTOTP(_ context.Context, userID int64, hash [sha256.Size]byte, update func(EnrollmentState) (EnrollmentChange, error)) error {
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

// enrollmentFixture is an enrollment whose code was checked a minute after it
// started, with codes waiting for the returned token.
func enrollmentFixture() (*Enrollment, *enrollmentStorage, Authenticated, string, *fakeClock) {
	clock := &fakeClock{now: time.Unix(1_000_000, 0).UTC()}
	issued := newSession(42, Device{}, enrollmentSessionLifetime, clock.Now)
	token, tokenHash := randomsecret.New()
	store := &enrollmentStorage{state: EnrollmentState{
		Session: issued.State,
		Factor:  &totp.Factor{KeyID: "2", Ciphertext: []byte("encrypted"), LastStep: 3, StartedAt: clock.now.Add(-time.Minute)},
		Waiting: &totp.Waiting{Hashes: []string{"a", "b"}, TokenHash: tokenHash, ExpiresAt: clock.now.Add(14 * time.Minute)},
	}}
	current := Authenticated{Identity: Identity{User: account.User{ID: 42}}, Session: Reference{hash: issued.State.TokenHash}}
	return NewEnrollment(store, clock.Now), store, current, token, clock
}

func TestFinishEnrollment(t *testing.T) {
	service, store, current, token, clock := enrollmentFixture()

	device := Device{UserAgent: "finishing browser", Addr: netip.MustParseAddr("198.51.100.4")}
	issued, err := service.Finish(t.Context(), current, token, device)
	require.NoError(t, err)

	require.NotNil(t, store.change)
	assert.Equal(t, device, store.change.Session.Device)
	assert.Equal(t, current.Identity.User.ID, store.userID)
	assert.Equal(t, current.Session.hash, store.hash)
	assert.Nil(t, store.state.Factor.ConfirmedAt, "the callback must not mutate stored input")
	require.NotNil(t, store.change.Factor.ConfirmedAt)
	assert.Equal(t, clock.Now(), *store.change.Factor.ConfirmedAt)
	assert.Equal(t, int64(3), store.change.Factor.LastStep)
	assert.Equal(t, []string{"a", "b"}, store.change.BackupHashes)
	assert.Equal(t, current.Identity.User.ID, issued.State.UserID)
	assert.Equal(t, clock.Now().Add(maxLifetime), issued.State.ExpiresAt, "the replacement is a full session")
	assert.NotEqual(t, current.Session.hash, issued.State.TokenHash)
	hash, err := randomsecret.Hash(issued.Secret)
	require.NoError(t, err)
	assert.Equal(t, issued.State.TokenHash, hash)
	assert.Equal(t, issued.State, store.change.Session)
}

func TestFinishEnrollmentRejected(t *testing.T) {
	failure := errors.New("storage unavailable")
	for _, c := range []struct {
		name  string
		edit  func(*enrollmentStorage, *fakeClock)
		token func(string) string
		want  error
	}{
		{"missing factor", func(s *enrollmentStorage, _ *fakeClock) { s.state.Factor = nil }, nil, totp.ErrNotPending},
		{"already on", func(s *enrollmentStorage, c *fakeClock) {
			now := c.Now()
			s.state.Factor.ConfirmedAt = &now
		}, nil, totp.ErrAlreadyOn},
		{"enrollment expired", func(_ *enrollmentStorage, c *fakeClock) { c.Advance(14 * time.Minute) }, nil, totp.ErrEnrollmentExpired},
		{"code not checked", func(s *enrollmentStorage, _ *fakeClock) { s.state.Waiting = nil }, nil, totp.ErrNotWaiting},
		{"wrong token", func(*enrollmentStorage, *fakeClock) {}, func(string) string { other, _ := randomsecret.New(); return other }, totp.ErrNotWaiting},
		{"malformed token", func(*enrollmentStorage, *fakeClock) {}, func(string) string { return "malformed" }, totp.ErrNotWaiting},
		{"expired session", func(s *enrollmentStorage, c *fakeClock) { s.state.Session.ExpiresAt = c.Now() }, nil, ErrUnauthenticated},
		{"wrong owner", func(s *enrollmentStorage, _ *fakeClock) { s.state.Session.UserID++ }, nil, ErrUnauthenticated},
		{"wrong session", func(s *enrollmentStorage, _ *fakeClock) { s.state.Session.TokenHash[0]++ }, nil, ErrUnauthenticated},
		{"missing session", func(s *enrollmentStorage, _ *fakeClock) { s.readErr = ErrNotFound }, nil, ErrUnauthenticated},
		{"storage", func(s *enrollmentStorage, _ *fakeClock) { s.readErr = failure }, nil, failure},
		{"commit", func(s *enrollmentStorage, _ *fakeClock) { s.commitErr = failure }, nil, failure},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, current, token, clock := enrollmentFixture()
			c.edit(store, clock)
			if c.token != nil {
				token = c.token(token)
			}

			issued, err := service.Finish(t.Context(), current, token, Device{})

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, issued, "failed finishing must reveal no session")
			assert.Nil(t, store.change)
		})
	}
}

func TestFinishEnrollmentUnchecked(t *testing.T) {
	for _, current := range []Authenticated{
		{},
		{Identity: Identity{User: account.User{ID: 42}}},
		{Session: Reference{hash: sha256.Sum256([]byte("unchecked"))}},
	} {
		service, store, _, token, _ := enrollmentFixture()

		issued, err := service.Finish(t.Context(), current, token, Device{})

		require.ErrorIs(t, err, ErrUnauthenticated)
		assert.Empty(t, issued)
		assert.Zero(t, store.calls)
	}
}
