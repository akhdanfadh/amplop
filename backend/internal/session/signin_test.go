package session

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
)

type signInAccounts struct {
	authenticate func(context.Context, string, string) (account.User, error)
	userByID     func(context.Context, int64) (account.User, error)
}

func (f signInAccounts) UserByID(ctx context.Context, id int64) (account.User, error) {
	return f.userByID(ctx, id)
}

func (f signInAccounts) Authenticate(ctx context.Context, email, password string) (account.User, error) {
	return f.authenticate(ctx, email, password)
}

type signInStorage struct {
	Storage
	states []State
	err    error
}

func (f *signInStorage) InsertSession(_ context.Context, state State) error {
	if f.err != nil {
		return f.err
	}
	f.states = append(f.states, state)
	return nil
}

type signInAttempts struct {
	*memoryAttempts
	reserveErr error
	deleteErr  error
}

func (f *signInAttempts) ReserveAttempt(ctx context.Context, attempt Attempt, cutoff time.Time, check func([]Attempt) error) (int64, error) {
	if f.reserveErr != nil {
		return 0, f.reserveErr
	}
	return f.memoryAttempts.ReserveAttempt(ctx, attempt, cutoff, check)
}

func (f *signInAttempts) DeleteAttempt(ctx context.Context, id int64) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	return f.memoryAttempts.DeleteAttempt(ctx, id)
}

func TestSignIn(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)}
	store := &signInStorage{}
	attempts := &memoryAttempts{rows: map[int64]Attempt{}}
	throttle := NewThrottle(attempts, clock.Now)
	addr := netip.MustParseAddr("192.0.2.1")
	for range 4 {
		_, err := throttle.Begin(t.Context(), "admin@example.com", addr)
		require.NoError(t, err)
	}
	accounts := signInAccounts{authenticate: func(_ context.Context, email, password string) (account.User, error) {
		assert.Len(t, attempts.rows, 5, "reserve before checking credentials")
		assert.Equal(t, "ADMIN@Example.COM", email)
		assert.Equal(t, " password with spaces ", password)
		clock.Advance(time.Second)
		return account.User{ID: 42, Email: "Admin@example.com"}, nil
	}}
	service := NewService(store, accounts, throttle, clock.Now)

	first, err := service.SignIn(t.Context(), "ADMIN@Example.COM", " password with spaces ", addr)

	require.NoError(t, err)
	require.Len(t, store.states, 1)
	assert.Equal(t, first.State, store.states[0])
	assert.Equal(t, int64(42), first.State.UserID)
	assert.Equal(t, clock.Now(), first.State.CreatedAt, "lifetime starts after credential verification")
	hash, err := hashSecret(first.Secret)
	require.NoError(t, err)
	assert.Equal(t, hash, first.State.TokenHash)
	assert.Len(t, attempts.rows, 4, "remove only the successful reservation")

	second, err := service.SignIn(t.Context(), "ADMIN@Example.COM", " password with spaces ", addr)
	require.NoError(t, err)
	assert.NotEqual(t, first.Secret, second.Secret)
	assert.Len(t, store.states, 2)
}

func TestSignInThrottled(t *testing.T) {
	attempts := &memoryAttempts{rows: map[int64]Attempt{}}
	throttle := NewThrottle(attempts, time.Now)
	addr := netip.MustParseAddr("192.0.2.1")
	for range 5 {
		_, err := throttle.Begin(t.Context(), "admin@example.com", addr)
		require.NoError(t, err)
	}
	accounts := signInAccounts{authenticate: func(context.Context, string, string) (account.User, error) {
		t.Error("throttled attempt reached password verification")
		return account.User{ID: 42}, nil
	}}
	store := &signInStorage{}

	issued, err := NewService(store, accounts, throttle, time.Now).SignIn(t.Context(), "admin@example.com", "password", addr)

	require.ErrorIs(t, err, ErrThrottled)
	assert.Zero(t, issued)
	assert.Empty(t, store.states)
	assert.Len(t, attempts.rows, 5)
}

func TestSignInErrors(t *testing.T) {
	lost := errors.New("connection lost")
	cases := []struct {
		name        string
		reserveErr  error
		authErr     error
		deleteErr   error
		insertErr   error
		want        error
		wantChecks  int
		wantPending int
	}{
		{"reserve", lost, nil, nil, nil, lost, 0, 0},
		{"credentials", nil, account.ErrInvalidCredentials, nil, nil, account.ErrInvalidCredentials, 1, 1},
		{"account storage", nil, lost, nil, nil, lost, 1, 1},
		{"remove attempt", nil, nil, lost, nil, lost, 1, 1},
		{"insert session", nil, nil, nil, lost, lost, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			attempts := &signInAttempts{
				memoryAttempts: &memoryAttempts{rows: map[int64]Attempt{}},
				reserveErr:     c.reserveErr, deleteErr: c.deleteErr,
			}
			checks := 0
			accounts := signInAccounts{authenticate: func(context.Context, string, string) (account.User, error) {
				checks++
				return account.User{ID: 42}, c.authErr
			}}
			store := &signInStorage{err: c.insertErr}
			service := NewService(store, accounts, NewThrottle(attempts, time.Now), time.Now)

			issued, err := service.SignIn(t.Context(), "admin@example.com", "password", netip.MustParseAddr("192.0.2.1"))

			require.ErrorIs(t, err, c.want)
			assert.Zero(t, issued)
			assert.Empty(t, store.states)
			assert.Equal(t, c.wantChecks, checks)
			assert.Len(t, attempts.rows, c.wantPending)
		})
	}
}
