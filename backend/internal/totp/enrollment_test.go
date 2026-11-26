package totp

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/account"
	"amplop/internal/randomsecret"
)

var (
	alice     = account.User{ID: 42, Email: "alice@example.com", Role: account.RoleMember}
	aliceAddr = netip.MustParseAddr("192.0.2.1")
)

// enrollmentFixture is a service at Unix 59, when "287082" is the vector
// secret's code, with fakes the test can inspect.
func enrollmentFixture() (*Service, *memoryTOTP, *fakeCipher, *fakePasswords, *backupAttempts, *fakeClock) {
	clock := &fakeClock{now: time.Unix(59, 0).UTC()}
	store, cipher, passwords, attempts := &memoryTOTP{}, &fakeCipher{}, &fakePasswords{}, &backupAttempts{}
	return NewService(store, cipher, passwords, NewVerifier(clock.Now), attempts, clock.Now), store, cipher, passwords, attempts, clock
}

// pendingFactor is the vector secret's enrollment for user 42, started at.
func pendingFactor(at time.Time) *Factor {
	return &Factor{KeyID: "1", Ciphertext: sealedSecret, LastStep: -1, StartedAt: at}
}

func TestStartEnrollment(t *testing.T) {
	service, store, cipher, passwords, _, clock := enrollmentFixture()

	first, err := service.Start(t.Context(), alice, "right", aliceAddr)

	require.NoError(t, err)
	require.NotNil(t, store.record.Factor)
	assert.Len(t, first.Secret, encodedSecretSize)
	assert.Contains(t, first.URI, "otpauth://totp/")
	assert.Contains(t, first.URI, "secret="+first.Secret)
	assert.Equal(t, []string{"totp:42"}, cipher.sealed)
	assert.Equal(t, append([]byte("totp:42|"), first.Secret...), store.record.Factor.Ciphertext)
	assert.Nil(t, store.record.Factor.ConfirmedAt)
	assert.Equal(t, int64(-1), store.record.Factor.LastStep)
	assert.Equal(t, clock.now, store.record.Factor.StartedAt)
	assert.Equal(t, 1, passwords.calls)

	// Continuing within the enrollment's time shows the same entry and saves
	// nothing, so the clock does not restart.
	saved := store.record.Factor
	clock.Advance(enrollmentLifetime - time.Second)
	second, err := service.Start(t.Context(), alice, "right", aliceAddr)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Same(t, saved, store.record.Factor)

	// An expired enrollment is replaced, with a new clock.
	clock.Advance(time.Second)
	third, err := service.Start(t.Context(), alice, "right", aliceAddr)
	require.NoError(t, err)
	assert.NotEqual(t, first.Secret, third.Secret)
	assert.Equal(t, clock.now, store.record.Factor.StartedAt)
}

func TestStartEnrollmentReplacesExpiredWaitingCodes(t *testing.T) {
	service, store, _, _, _, clock := enrollmentFixture()
	store.record = Record{
		Factor:  pendingFactor(clock.now.Add(-enrollmentLifetime)),
		Waiting: &Waiting{Hashes: []string{"old"}, ExpiresAt: clock.now},
	}

	_, err := service.Start(t.Context(), alice, "right", aliceAddr)

	require.NoError(t, err)
	assert.Nil(t, store.record.Waiting, "codes for the old secret go with it")
}

func TestStartEnrollmentRefused(t *testing.T) {
	confirmed := time.Unix(1, 0)
	for _, c := range []struct {
		name     string
		record   Record
		password string
		want     error
	}{
		{"wrong password", Record{}, "wrong", account.ErrInvalidCredentials},
		{"already on", Record{Factor: &Factor{Ciphertext: sealedSecret, ConfirmedAt: &confirmed}}, "right", ErrAlreadyOn},
		{"code checked", Record{Factor: pendingFactor(time.Unix(59, 0)), Waiting: &Waiting{ExpiresAt: time.Unix(59, 0).Add(enrollmentLifetime)}}, "right", ErrCodeChecked},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, cipher, _, _, _ := enrollmentFixture()
			store.record = c.record

			started, err := service.Start(t.Context(), alice, c.password, aliceAddr)

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, started)
			assert.Equal(t, c.record, store.record)
			if c.name == "wrong password" {
				assert.Zero(t, store.calls, "nothing is made before the password is right")
				assert.Empty(t, cipher.sealed)
			}
		})
	}
}

func TestStartEnrollmentFailure(t *testing.T) {
	failure := errors.New("storage, encryption, or password check unavailable")
	for _, stage := range []string{"password", "seal", "storage"} {
		t.Run(stage, func(t *testing.T) {
			service, store, cipher, passwords, _, _ := enrollmentFixture()
			switch stage {
			case "password":
				passwords.err = failure
			case "seal":
				cipher.sealErr = failure
			case "storage":
				store.readErr = failure
			}

			started, err := service.Start(t.Context(), alice, "right", aliceAddr)

			require.ErrorIs(t, err, failure)
			assert.Empty(t, started, "failed enrollment must not return a secret")
			assert.Nil(t, store.record.Factor)
		})
	}
}

func TestVerifyCode(t *testing.T) {
	service, store, cipher, _, attempts, clock := enrollmentFixture()
	started := clock.now.Add(-time.Minute)
	store.record.Factor = pendingFactor(started)

	codes, err := service.VerifyCode(t.Context(), 42, "287082")

	require.NoError(t, err)
	require.Len(t, codes.Codes, backupSetSize)
	waiting := store.record.Waiting
	require.NotNil(t, waiting)
	require.Len(t, waiting.Hashes, backupSetSize)
	for i, code := range codes.Codes {
		require.NoError(t, VerifyBackupCode(t.Context(), code, waiting.Hashes[i]))
	}
	assert.Empty(t, store.backupHashes, "the codes wait; none is active yet")
	assert.Nil(t, store.record.Factor.ConfirmedAt, "two-factor stays off")
	assert.Equal(t, int64(1), store.record.Factor.LastStep, "the code is used up")
	assert.Equal(t, started.Add(enrollmentLifetime), waiting.ExpiresAt, "the codes wait as long as the enrollment")
	assert.Equal(t, []string{"totp-backup-codes:42"}, cipher.sealed)
	assert.Equal(t, []byte("totp-backup-codes:42|"+strings.Join(codes.Codes, "\n")), waiting.Ciphertext)
	tokenHash, err := randomsecret.Hash(codes.Token)
	require.NoError(t, err)
	assert.Equal(t, tokenHash, waiting.TokenHash)
	assert.Equal(t, []int64{7}, store.clearedAttempts)
	assert.Empty(t, attempts.released)
}

func TestVerifyCodeRefused(t *testing.T) {
	now := time.Unix(59, 0).UTC()
	// wantRelease means the request checked no code, so it releases its attempt.
	for _, c := range []struct {
		name        string
		record      Record
		code        string
		want        error
		wantRelease bool
	}{
		{"not started", Record{}, "287082", ErrNotPending, true},
		{"already on", Record{Factor: &Factor{Ciphertext: sealedSecret, ConfirmedAt: &now}}, "287082", ErrAlreadyOn, true},
		{"expired", Record{Factor: pendingFactor(now.Add(-enrollmentLifetime))}, "287082", ErrEnrollmentExpired, true},
		{"code checked", Record{Factor: pendingFactor(now), Waiting: &Waiting{ExpiresAt: now.Add(enrollmentLifetime)}}, "287082", ErrCodeChecked, true},
		{"wrong", Record{Factor: pendingFactor(now)}, "000000", ErrInvalidCode, false},
		{"replay", Record{Factor: &Factor{Ciphertext: sealedSecret, LastStep: 1, StartedAt: now}}, "287082", ErrInvalidCode, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, _, _, attempts, _ := enrollmentFixture()
			store.record = c.record

			codes, err := service.VerifyCode(t.Context(), 42, c.code)

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, codes)
			assert.Equal(t, c.record, store.record)
			if c.wantRelease {
				assert.Equal(t, []int64{7}, attempts.released)
			} else {
				assert.Empty(t, attempts.released)
			}
		})
	}
}

func TestVerifyCodeThrottled(t *testing.T) {
	service, store, _, _, attempts, clock := enrollmentFixture()
	store.record.Factor = pendingFactor(clock.now)
	attempts.err = ErrThrottled

	_, err := service.VerifyCode(t.Context(), 42, "287082")

	require.ErrorIs(t, err, ErrThrottled)
	assert.Zero(t, store.calls)
}

func TestShowWaitingCodes(t *testing.T) {
	service, store, _, passwords, _, clock := enrollmentFixture()
	store.record.Factor = pendingFactor(clock.now)
	first, err := service.VerifyCode(t.Context(), 42, "287082")
	require.NoError(t, err)

	again, err := service.ShowWaiting(t.Context(), alice, "right", aliceAddr)

	require.NoError(t, err)
	assert.Equal(t, first.Codes, again.Codes, "a reload shows the same codes")
	assert.NotEqual(t, first.Token, again.Token)
	require.ErrorIs(t, VerifyWaiting(store.record.Waiting, first.Token, clock.now), ErrNotWaiting, "showing again replaces the token")
	require.NoError(t, VerifyWaiting(store.record.Waiting, again.Token, clock.now))
	assert.Equal(t, 1, passwords.calls)
}

func TestShowWaitingCodesRefused(t *testing.T) {
	now := time.Unix(59, 0).UTC()
	waiting := &Waiting{ExpiresAt: now.Add(time.Minute)}
	for _, c := range []struct {
		name     string
		record   Record
		password string
		want     error
	}{
		{"wrong password", Record{Factor: pendingFactor(now), Waiting: waiting}, "wrong", account.ErrInvalidCredentials},
		{"no enrollment", Record{}, "right", ErrNotWaiting},
		{"code not checked", Record{Factor: pendingFactor(now)}, "right", ErrNotWaiting},
		{"enrollment expired", Record{Factor: pendingFactor(now.Add(-enrollmentLifetime)), Waiting: waiting}, "right", ErrEnrollmentExpired},
		{"no display copy", Record{Factor: pendingFactor(now), Waiting: waiting}, "right", ErrNotWaiting},
		// Replacement codes appear only in Replace's answer.
		{"two-factor on", Record{Factor: &Factor{ConfirmedAt: &now}, Waiting: waiting}, "right", ErrAlreadyOn},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, _, _, _, _ := enrollmentFixture()
			store.record = c.record

			codes, err := service.ShowWaiting(t.Context(), alice, c.password, aliceAddr)

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, codes)
			assert.Equal(t, c.record, store.record)
		})
	}
}

func TestEnrollmentStatus(t *testing.T) {
	now := time.Unix(59, 0).UTC()
	live := &Waiting{ExpiresAt: now.Add(time.Second)}
	ended := &Waiting{ExpiresAt: now}
	for _, c := range []struct {
		name        string
		record      Record
		codes       int
		want        State
		wantCount   int
		wantWaiting bool
	}{
		{"off", Record{}, 0, StateOff, 0, false},
		{"pending", Record{Factor: pendingFactor(now)}, 0, StatePending, 0, false},
		{"pending hides codes", Record{Factor: pendingFactor(now)}, 10, StatePending, 0, false},
		{"code checked", Record{Factor: pendingFactor(now), Waiting: live}, 0, StatePending, 0, true},
		{"expired", Record{Factor: pendingFactor(now.Add(-enrollmentLifetime)), Waiting: ended}, 0, StateExpired, 0, false},
		{"on", Record{Factor: &Factor{ConfirmedAt: &now}}, 3, StateOn, 3, false},
		{"on replacing", Record{Factor: &Factor{ConfirmedAt: &now}, Waiting: live}, 10, StateOn, 10, true},
		{"on, replacement expired", Record{Factor: &Factor{ConfirmedAt: &now}, Waiting: ended}, 10, StateOn, 10, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, _, _, _, _ := enrollmentFixture()
			store.record, store.backupHashes = c.record, make([]string, c.codes)

			status, err := service.Status(t.Context(), 42)

			require.NoError(t, err)
			assert.Equal(t, Status{State: c.want, BackupCodesLeft: c.wantCount, BackupCodesWaiting: c.wantWaiting}, status)
		})
	}
}

func TestEnrollmentStatusFailure(t *testing.T) {
	service, store, _, _, _, _ := enrollmentFixture()
	failure := errors.New("storage unavailable")
	store.readErr = failure

	status, err := service.Status(t.Context(), 42)

	require.ErrorIs(t, err, failure)
	assert.Empty(t, status)
}

func TestVerifyWaiting(t *testing.T) {
	now := time.Unix(59, 0)
	token, hash := randomsecret.New()
	other, _ := randomsecret.New()
	waiting := &Waiting{TokenHash: hash, ExpiresAt: now.Add(time.Second)}

	require.NoError(t, VerifyWaiting(waiting, token, now))
	require.ErrorIs(t, VerifyWaiting(waiting, other, now), ErrNotWaiting)
	require.ErrorIs(t, VerifyWaiting(waiting, "malformed", now), ErrNotWaiting)
	require.ErrorIs(t, VerifyWaiting(waiting, token, now.Add(time.Second)), ErrNotWaiting, "expiry is exclusive")
	require.ErrorIs(t, VerifyWaiting(nil, token, now), ErrNotWaiting)
}

func TestCancelEnrollment(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name    string
		record  Record
		wantErr error
	}{
		{"pending", Record{Factor: &Factor{KeyID: "1", Ciphertext: []byte("pending"), LastStep: -1}}, nil},
		{"code checked", Record{Factor: &Factor{KeyID: "1", LastStep: 1}, Waiting: &Waiting{}}, nil},
		{"already absent", Record{}, nil},
		{"confirmed", Record{Factor: &Factor{KeyID: "1", Ciphertext: []byte("confirmed"), ConfirmedAt: &now}}, ErrAlreadyOn},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, _, _, _, _ := enrollmentFixture()
			store.record = c.record

			err := service.Cancel(t.Context(), 42)

			require.ErrorIs(t, err, c.wantErr)
			if c.wantErr != nil {
				assert.Equal(t, c.record, store.record)
				return
			}
			assert.Equal(t, Record{}, store.record)
		})
	}
}

func TestCancelEnrollmentFailure(t *testing.T) {
	service, store, _, _, _, _ := enrollmentFixture()
	failure := errors.New("cancellation storage unavailable")
	original := Record{Factor: &Factor{Ciphertext: []byte("pending")}}
	store.record, store.readErr = original, failure

	err := service.Cancel(t.Context(), 42)

	require.ErrorIs(t, err, failure)
	assert.Equal(t, original, store.record)
}

func TestCancelEnrollmentInvalidUser(t *testing.T) {
	service, _, _, _, _, _ := enrollmentFixture()
	for _, userID := range []int64{0, -1} {
		require.ErrorIs(t, service.Cancel(t.Context(), userID), account.ErrUserNotFound)
	}
}
