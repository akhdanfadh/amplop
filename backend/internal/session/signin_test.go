package session

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/account"
	"amplop/internal/emailaddress"
	"amplop/internal/randomsecret"
	"amplop/internal/totp"
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
	role         account.Role
	passwordHash string
	deactivated  bool
	err          error
}

func (f *signInStorage) StartSignIn(_ context.Context, _ int64, update func(SignInState) (SignInChange, error)) error {
	next, err := update(SignInState{PasswordHash: f.passwordHash, Role: f.role, Factor: f.factor, Deactivated: f.deactivated})
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
		assert.Equal(t, "admin@example.com", email, "the throttle and accounts see the parsed address")
		assert.Equal(t, " password with spaces ", password)
		clock.Advance(time.Second)
		return account.Credentials{User: account.User{ID: 42, Email: "admin@example.com"}}, nil
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
	hash, err := randomsecret.Hash(first.Secret)
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

func TestStartSignInInvalidEmail(t *testing.T) {
	attempts := &memoryAttempts{rows: map[int64]Attempt{}}
	accounts := signInAccounts{authenticate: func(context.Context, string, string) (account.Credentials, error) {
		t.Error("an invalid email reached password verification")
		return account.Credentials{}, nil
	}}
	service := NewService(&signInStorage{}, accounts, NewThrottle(attempts, time.Now), time.Now)

	issued, err := service.StartSignIn(t.Context(), "admin.example.com", "password", Device{Addr: netip.MustParseAddr("192.0.2.1")})

	require.ErrorIs(t, err, emailaddress.ErrInvalid)
	assert.Zero(t, issued)
	assert.Empty(t, attempts.rows, "no attempt is reserved")
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
		name     string
		role     account.Role
		factor   *totp.Factor
		step     SignInStep
		lifetime time.Duration
	}{
		{"member missing", account.RoleMember, nil, SignInStepComplete, maxLifetime},
		{"member pending", account.RoleMember, &totp.Factor{}, SignInStepComplete, maxLifetime},
		{"member confirmed", account.RoleMember, &totp.Factor{ConfirmedAt: &clock.now}, SignInStepTOTP, 0},
		// An administrator without two-factor gets a short enrollment session.
		{"admin missing", account.RoleAdmin, nil, SignInStepComplete, enrollmentSessionLifetime},
		{"admin pending", account.RoleAdmin, &totp.Factor{}, SignInStepComplete, enrollmentSessionLifetime},
		{"admin confirmed", account.RoleAdmin, &totp.Factor{ConfirmedAt: &clock.now}, SignInStepTOTP, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			store := &signInStorage{factor: c.factor, role: c.role}
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
				assert.Equal(t, clock.Now().Add(c.lifetime), store.states[0].ExpiresAt)
				assert.Equal(t, store.states[0].ExpiresAt, result.ExpiresAt)
				return
			}
			assert.Empty(t, store.states)
			require.Len(t, store.challenges, 1)
			hash, err := randomsecret.Hash(result.Secret)
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

// The right password for a deactivated account issues neither a session nor
// a challenge, and says the account is deactivated. The password was right,
// so the attempt is not a failure.
func TestStartSignInDeactivated(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name   string
		factor *totp.Factor
	}{
		{"session", nil},
		{"challenge", &totp.Factor{ConfirmedAt: &now}},
	} {
		t.Run(c.name, func(t *testing.T) {
			store := &signInStorage{passwordHash: "hash", factor: c.factor, deactivated: true}
			accounts := signInAccounts{authenticate: func(context.Context, string, string) (account.Credentials, error) {
				return account.Credentials{User: account.User{ID: 42}, PasswordHash: "hash"}, nil
			}}
			attempts := &memoryAttempts{rows: map[int64]Attempt{}}
			throttle := NewThrottle(attempts, time.Now)

			issued, err := NewService(store, accounts, throttle, time.Now).StartSignIn(t.Context(), "member@example.com", "password", Device{Addr: netip.MustParseAddr("192.0.2.1")})

			require.ErrorIs(t, err, account.ErrDeactivated)
			assert.Zero(t, issued)
			assert.Empty(t, store.states)
			assert.Empty(t, store.challenges)
			assert.Empty(t, attempts.rows)
		})
	}
}

// A stale password is refused as invalid even for a deactivated account, so
// only current password proof learns that the account is deactivated.
func TestStartSignInDeactivatedStalePassword(t *testing.T) {
	store := &signInStorage{passwordHash: "replacement hash", deactivated: true}
	accounts := signInAccounts{authenticate: func(context.Context, string, string) (account.Credentials, error) {
		return account.Credentials{User: account.User{ID: 42}, PasswordHash: "verified earlier hash"}, nil
	}}
	throttle := NewThrottle(&memoryAttempts{rows: map[int64]Attempt{}}, time.Now)

	_, err := NewService(store, accounts, throttle, time.Now).StartSignIn(t.Context(), "member@example.com", "password", Device{Addr: netip.MustParseAddr("192.0.2.1")})

	require.ErrorIs(t, err, account.ErrInvalidCredentials)
}

func TestSignInFirstAdmin(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)}
	store := &signInStorage{role: account.RoleAdmin}
	service := NewService(store, nil, nil, clock.Now)
	device := Device{UserAgent: "setup browser", Addr: netip.MustParseAddr("192.0.2.1")}

	result, err := service.SignInFirstAdmin(t.Context(), 42, device)

	require.NoError(t, err)
	assert.Equal(t, SignInStepComplete, result.Step)
	require.Len(t, store.states, 1)
	assert.Equal(t, clock.Now().Add(enrollmentSessionLifetime), store.states[0].ExpiresAt, "enrollment access only")
	assert.Equal(t, store.states[0].ExpiresAt, result.ExpiresAt)
	assert.Equal(t, device, store.states[0].Device)
	hash, err := randomsecret.Hash(result.Secret)
	require.NoError(t, err)
	assert.Equal(t, hash, store.states[0].TokenHash)
}

func TestSignInFirstAdminRefused(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name  string
		store *signInStorage
	}{
		{"member", &signInStorage{role: account.RoleMember}},
		{"deactivated", &signInStorage{role: account.RoleAdmin, deactivated: true}},
		{"two-factor on", &signInStorage{role: account.RoleAdmin, factor: &totp.Factor{ConfirmedAt: &now}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			result, err := NewService(c.store, nil, nil, time.Now).SignInFirstAdmin(t.Context(), 42, Device{})

			require.ErrorIs(t, err, ErrNotFound)
			assert.Empty(t, result)
			assert.Empty(t, c.store.states)
		})
	}
}

func TestVerifyPassword(t *testing.T) {
	addr := netip.MustParseAddr("192.0.2.1")
	alice := account.User{ID: 42, Email: "alice@example.com"}
	for _, c := range []struct {
		name        string
		password    string
		owner       int64
		want        error
		wantPending int
	}{
		{"right", "right", 42, nil, 0},
		{"wrong", "wrong", 42, account.ErrInvalidCredentials, 1},
		{"another account's", "right", 7, account.ErrInvalidCredentials, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			attempts := &memoryAttempts{rows: map[int64]Attempt{}}
			accounts := signInAccounts{authenticate: func(_ context.Context, email, password string) (account.Credentials, error) {
				assert.Equal(t, alice.Email, email)
				assert.Len(t, attempts.rows, 1, "reserve before checking the password")
				if password != "right" {
					return account.Credentials{}, account.ErrInvalidCredentials
				}
				return account.Credentials{User: account.User{ID: c.owner}}, nil
			}}
			service := NewService(nil, accounts, NewThrottle(attempts, time.Now), time.Now)

			err := service.VerifyPassword(t.Context(), alice, c.password, addr)

			if c.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, c.want)
			}
			assert.Len(t, attempts.rows, c.wantPending, "only failures count")
		})
	}
}

func TestVerifyPasswordThrottled(t *testing.T) {
	attempts := &memoryAttempts{rows: map[int64]Attempt{}}
	throttle := NewThrottle(attempts, time.Now)
	addr := netip.MustParseAddr("192.0.2.1")
	for range 5 {
		_, err := throttle.Begin(t.Context(), "alice@example.com", addr)
		require.NoError(t, err)
	}
	accounts := signInAccounts{authenticate: func(context.Context, string, string) (account.Credentials, error) {
		t.Fatal("a throttled check must not hash the password")
		return account.Credentials{}, nil
	}}

	err := NewService(nil, accounts, throttle, time.Now).VerifyPassword(t.Context(), account.User{ID: 42, Email: "alice@example.com"}, "right", addr)

	require.ErrorIs(t, err, ErrThrottled)
}
