package totp

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backupFixture is a confirmed factor for user 42 with two active codes, at
// Unix 59, when "287082" is the vector secret's code.
func backupFixture() (*Backups, *memoryTOTP, *fakeCipher, *backupAttempts, *fakeClock) {
	clock := &fakeClock{now: time.Unix(59, 0).UTC()}
	confirmed := clock.now.Add(-time.Hour)
	store := &memoryTOTP{
		record:       Record{Factor: &Factor{KeyID: "1", Ciphertext: sealedSecret, ConfirmedAt: &confirmed, LastStep: -1}},
		backupHashes: []string{"old-1", "old-2"},
	}
	cipher, attempts := &fakeCipher{}, &backupAttempts{}
	return NewBackups(store, cipher, NewVerifier(clock.Now), attempts, clock.Now), store, cipher, attempts, clock
}

func TestReplaceBackupCodes(t *testing.T) {
	service, store, _, attempts, clock := backupFixture()
	loaded := store.record.Factor

	codes, err := service.Replace(t.Context(), 42, "287082")

	require.NoError(t, err)
	require.Len(t, codes.Codes, backupSetSize)
	waiting := store.record.Waiting
	require.NotNil(t, waiting)
	for i, code := range codes.Codes {
		require.NoError(t, VerifyBackupCode(t.Context(), code, waiting.Hashes[i]))
	}
	assert.Equal(t, []string{"old-1", "old-2"}, store.backupHashes, "the old codes work until finish")
	assert.Equal(t, clock.now.Add(waitingLifetime), waiting.ExpiresAt)
	require.NoError(t, VerifyWaiting(waiting, codes.Token, clock.now))
	assert.Equal(t, int64(42), store.userID)
	assert.Equal(t, int64(1), store.record.Factor.LastStep)
	assert.Equal(t, int64(-1), loaded.LastStep, "do not mutate the loaded factor before commit")
	assert.Equal(t, loaded.ConfirmedAt, store.record.Factor.ConfirmedAt)
	assert.Equal(t, []int64{7}, store.clearedAttempts)
	assert.Equal(t, 1, attempts.calls)
}

func TestReplaceBackupCodesTwice(t *testing.T) {
	service, store, _, _, clock := backupFixture()
	first, err := service.Replace(t.Context(), 42, "287082")
	require.NoError(t, err)

	clock.Advance(30 * time.Second)
	second, err := service.Replace(t.Context(), 42, "359152")

	require.NoError(t, err)
	assert.NotEqual(t, first.Codes, second.Codes)
	require.ErrorIs(t, VerifyWaiting(store.record.Waiting, first.Token, clock.now), ErrNotWaiting, "a new set replaces the waiting one")
}

func TestReplaceBackupCodesRejected(t *testing.T) {
	// wantRelease means the request checked no code, so it releases its attempt.
	for _, c := range []struct {
		name        string
		edit        func(*memoryTOTP)
		code        string
		want        error
		wantRelease bool
	}{
		{"missing", func(s *memoryTOTP) { s.record.Factor = nil }, "287082", ErrNotOn, true},
		{"pending", func(s *memoryTOTP) { s.record.Factor.ConfirmedAt = nil }, "287082", ErrNotOn, true},
		{"wrong", func(*memoryTOTP) {}, "000000", ErrInvalidCode, false},
		{"backup proof", func(*memoryTOTP) {}, "ABCDE-FGHJK", ErrInvalidCode, false},
		{"replay", func(s *memoryTOTP) { s.record.Factor.LastStep = 1 }, "287082", ErrInvalidCode, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, _, attempts, _ := backupFixture()
			c.edit(store)

			codes, err := service.Replace(t.Context(), 42, c.code)

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, codes)
			assert.Nil(t, store.record.Waiting)
			assert.Equal(t, 1, attempts.calls)
			if c.wantRelease {
				assert.Equal(t, []int64{7}, attempts.released)
			} else {
				assert.Empty(t, attempts.released)
			}
		})
	}
}

func TestReplaceBackupCodesErrors(t *testing.T) {
	lost := errors.New("storage or encryption failure")
	for _, stage := range []string{"throttle", "read", "open", "commit"} {
		t.Run(stage, func(t *testing.T) {
			service, store, cipher, attempts, _ := backupFixture()
			switch stage {
			case "throttle":
				attempts.err = ErrThrottled
			case "read":
				store.readErr = lost
			case "open":
				cipher.openErr = lost
			case "commit":
				store.commitErr = lost
			}

			codes, err := service.Replace(t.Context(), 42, "287082")

			if stage == "throttle" {
				require.ErrorIs(t, err, ErrThrottled)
				assert.Zero(t, store.calls)
				assert.Zero(t, cipher.opens)
			} else {
				require.ErrorIs(t, err, lost)
			}
			assert.Empty(t, codes, "never expose replacement codes on failure")
			assert.Nil(t, store.record.Waiting)
			// Only a failure before any code check gives the attempt back.
			if stage == "read" || stage == "open" {
				assert.Equal(t, []int64{7}, attempts.released)
			} else {
				assert.Empty(t, attempts.released)
			}
		})
	}
}

func TestFinishBackupReplacement(t *testing.T) {
	service, store, _, _, _ := backupFixture()
	codes, err := service.Replace(t.Context(), 42, "287082")
	require.NoError(t, err)
	hashes := store.record.Waiting.Hashes

	active, err := service.Finish(t.Context(), 42, codes.Token)

	require.NoError(t, err)
	assert.Equal(t, backupSetSize, active)
	assert.Equal(t, hashes, store.backupHashes)
	assert.Nil(t, store.record.Waiting)
}

func TestFinishBackupReplacementRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*memoryTOTP, *fakeClock)
		want error
	}{
		{"wrong token", func(s *memoryTOTP, _ *fakeClock) { s.record.Waiting.TokenHash[0] ^= 1 }, ErrNotWaiting},
		{"expired", func(_ *memoryTOTP, c *fakeClock) { c.Advance(waitingLifetime) }, ErrNotWaiting},
		{"nothing waiting", func(s *memoryTOTP, _ *fakeClock) { s.record.Waiting = nil }, ErrNotWaiting},
		{"turned off", func(s *memoryTOTP, _ *fakeClock) { s.record.Factor.ConfirmedAt = nil }, ErrNotOn},
	} {
		t.Run(c.name, func(t *testing.T) {
			service, store, _, _, clock := backupFixture()
			codes, err := service.Replace(t.Context(), 42, "287082")
			require.NoError(t, err)
			c.edit(store, clock)

			active, err := service.Finish(t.Context(), 42, codes.Token)

			require.ErrorIs(t, err, c.want)
			assert.Zero(t, active)
			assert.Equal(t, []string{"old-1", "old-2"}, store.backupHashes)
		})
	}
}
