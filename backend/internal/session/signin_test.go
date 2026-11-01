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
	"inbok/internal/totp"
)

type signInAccounts struct {
	authenticate func(context.Context, string, string) (account.Credentials, error)
}

func (f signInAccounts) Authenticate(ctx context.Context, email, password string) (account.Credentials, error) {
	return f.authenticate(ctx, email, password)
}

type signInStorage struct {
	Storage
	states       []State
	expiries     []Expiry
	challenges   []Challenge
	factor       *totp.Factor
	passwordHash string
	err          error
}

func (f *signInStorage) StartSignIn(_ context.Context, _ int64, update func(SignInState) (SignInChange, error)) error {
	next, err := update(SignInState{PasswordHash: f.passwordHash, Factor: f.factor})
	if err != nil {
		return err
	}
	if f.err != nil {
		return f.err
	}
	if next.Step == SignInStepComplete {
		f.states = append(f.states, next.Session)
		f.expiries = append(f.expiries, next.Expiry)
	} else {
		f.challenges = append(f.challenges, next.Challenge)
	}
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

func TestStartSignIn(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)}
	store := &signInStorage{}
	attempts := &memoryAttempts{rows: map[int64]Attempt{}}
	throttle := NewThrottle(attempts, clock.Now)
	addr := netip.MustParseAddr("192.0.2.1")
	for range 4 {
		_, err := throttle.Begin(t.Context(), "admin@example.com", addr)
		require.NoError(t, err)
	}
	accounts := signInAccounts{authenticate: func(_ context.Context, email, password string) (account.Credentials, error) {
		assert.Len(t, attempts.rows, 5, "reserve before checking credentials")
		assert.Equal(t, "ADMIN@Example.COM", email)
		assert.Equal(t, " password with spaces ", password)
		clock.Advance(time.Second)
		return account.Credentials{User: account.User{ID: 42, Email: "Admin@example.com"}}, nil
	}}
	service := NewService(store, accounts, throttle, clock.Now)

	device := Device{UserAgent: "Mozilla/5.0 (X11; Linux x86_64) Firefox/131.0", Addr: addr}
	first, err := service.StartSignIn(t.Context(), "ADMIN@Example.COM", " password with spaces ", device)

	require.NoError(t, err)
	require.Len(t, store.states, 1)
	assert.Equal(t, device, store.states[0].Device)
	assert.Equal(t, SignInStepComplete, first.Step)
	assert.Equal(t, first.ExpiresAt, store.states[0].ExpiresAt)
	assert.Equal(t, int64(42), store.states[0].UserID)
	assert.Equal(t, clock.Now(), store.states[0].CreatedAt, "lifetime starts after credential verification")
	assert.Equal(t, []Expiry{{ExpiresBy: clock.Now(), LastUsedBy: clock.Now().Add(-idleTimeout)}}, store.expiries,
		"the new session clears the user's expired ones")
	hash, err := hashSecret(first.Secret)
	require.NoError(t, err)
	assert.Equal(t, hash, store.states[0].TokenHash)
	assert.Len(t, attempts.rows, 4, "remove only the successful reservation")

	second, err := service.StartSignIn(t.Context(), "ADMIN@Example.COM", " password with spaces ", Device{Addr: addr})
	require.NoError(t, err)
	assert.NotEqual(t, first.Secret, second.Secret)
	assert.Len(t, store.states, 2)
}

func TestStartSignInThrottled(t *testing.T) {
	attempts := &memoryAttempts{rows: map[int64]Attempt{}}
	throttle := NewThrottle(attempts, time.Now)
	addr := netip.MustParseAddr("192.0.2.1")
	for range 5 {
		_, err := throttle.Begin(t.Context(), "admin@example.com", addr)
		require.NoError(t, err)
	}
	accounts := signInAccounts{authenticate: func(context.Context, string, string) (account.Credentials, error) {
		t.Error("throttled attempt reached password verification")
		return account.Credentials{User: account.User{ID: 42}}, nil
	}}
	store := &signInStorage{}

	issued, err := NewService(store, accounts, throttle, time.Now).StartSignIn(t.Context(), "admin@example.com", "password", Device{Addr: addr})

	require.ErrorIs(t, err, ErrThrottled)
	assert.Zero(t, issued)
	assert.Empty(t, store.states)
	assert.Len(t, attempts.rows, 5)
}

func TestStartSignInErrors(t *testing.T) {
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
			accounts := signInAccounts{authenticate: func(context.Context, string, string) (account.Credentials, error) {
				checks++
				return account.Credentials{User: account.User{ID: 42}}, c.authErr
			}}
			store := &signInStorage{err: c.insertErr}
			service := NewService(store, accounts, NewThrottle(attempts, time.Now), time.Now)

			issued, err := service.StartSignIn(t.Context(), "admin@example.com", "password", Device{Addr: netip.MustParseAddr("192.0.2.1")})

			require.ErrorIs(t, err, c.want)
			assert.Zero(t, issued)
			assert.Empty(t, store.states)
			assert.Equal(t, c.wantChecks, checks)
			assert.Len(t, attempts.rows, c.wantPending)
		})
	}
}

func TestPasswordRequiresTOTP(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)}
	for _, c := range []struct {
		name   string
		factor *totp.Factor
		step   SignInStep
	}{
		{"missing", nil, SignInStepComplete},
		{"pending", &totp.Factor{}, SignInStepComplete},
		{"confirmed", &totp.Factor{ConfirmedAt: &clock.now}, SignInStepTOTP},
	} {
		t.Run(c.name, func(t *testing.T) {
			store := &signInStorage{factor: c.factor}
			accounts := signInAccounts{authenticate: func(context.Context, string, string) (account.Credentials, error) {
				return account.Credentials{User: account.User{ID: 42}}, nil
			}}
			throttle := NewThrottle(&memoryAttempts{rows: map[int64]Attempt{}}, clock.Now)
			service := NewService(store, accounts, throttle, clock.Now)

			result, err := service.StartSignIn(t.Context(), "alice@example.com", "password", Device{Addr: netip.MustParseAddr("192.0.2.1")})

			require.NoError(t, err)
			assert.Equal(t, c.step, result.Step)
			if c.step == SignInStepComplete {
				assert.Empty(t, store.challenges)
				require.Len(t, store.states, 1)
				return
			}
			assert.Empty(t, store.states)
			require.Len(t, store.challenges, 1)
			hash, err := hashSecret(result.Secret)
			require.NoError(t, err)
			assert.Equal(t, hash, store.challenges[0].TokenHash)
			assert.Equal(t, clock.Now().Add(5*time.Minute), result.ExpiresAt)
		})
	}
}

func TestStartSignInStalePassword(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name   string
		factor *totp.Factor
	}{
		{"session", nil},
		{"challenge", &totp.Factor{ConfirmedAt: &now}},
	} {
		t.Run(c.name, func(t *testing.T) {
			store := &signInStorage{passwordHash: "replacement hash", factor: c.factor}
			accounts := signInAccounts{authenticate: func(context.Context, string, string) (account.Credentials, error) {
				return account.Credentials{User: account.User{ID: 42}, PasswordHash: "verified earlier hash"}, nil
			}}
			throttle := NewThrottle(&memoryAttempts{rows: map[int64]Attempt{}}, time.Now)

			issued, err := NewService(store, accounts, throttle, time.Now).StartSignIn(t.Context(), "admin@example.com", "password", Device{Addr: netip.MustParseAddr("192.0.2.1")})

			require.ErrorIs(t, err, account.ErrInvalidCredentials)
			assert.Zero(t, issued)
			assert.Empty(t, store.states)
			assert.Empty(t, store.challenges)
		})
	}
}
