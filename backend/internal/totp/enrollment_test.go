package totp

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
)

type fakeSealer struct {
	owner     string
	plaintext []byte
	err       error
}

func (f *fakeSealer) Seal(value []byte, owner string) (string, []byte, error) {
	f.owner, f.plaintext = owner, value
	return "1", []byte("encrypted"), f.err
}

func TestStartEnrollment(t *testing.T) {
	storage, sealer := &memoryTOTP{}, &fakeSealer{}
	service := NewService(storage, sealer, time.Now)
	user := account.User{ID: 42, Email: "alice@example.com", Role: account.RoleMember}

	first, err := service.Start(t.Context(), user)
	require.NoError(t, err)
	factor := storage.record.Factor
	require.NotNil(t, factor)
	assert.NotEmpty(t, first.Secret)
	assert.Contains(t, first.URI, "otpauth://totp/")
	assert.Equal(t, []byte(first.Secret), sealer.plaintext)
	assert.Equal(t, "totp:42", sealer.owner)
	assert.Equal(t, "1", factor.KeyID)
	assert.Equal(t, []byte("encrypted"), factor.Ciphertext)
	assert.Nil(t, factor.ConfirmedAt)
	assert.Equal(t, int64(-1), factor.LastStep)

	second, err := service.Start(t.Context(), user)
	require.NoError(t, err)
	assert.NotEqual(t, first.Secret, second.Secret, "restart replaces the pending secret")
	assert.Equal(t, []byte(second.Secret), sealer.plaintext)
}

func TestStartEnrollmentAlreadyOn(t *testing.T) {
	now := time.Now()
	original := &Factor{KeyID: "1", Ciphertext: []byte("original"), ConfirmedAt: &now, LastStep: 12}
	storage := &memoryTOTP{record: Record{Factor: original}}
	service := NewService(storage, &fakeSealer{}, time.Now)

	result, err := service.Start(t.Context(), account.User{ID: 42, Email: "alice@example.com"})
	require.ErrorIs(t, err, ErrAlreadyOn)
	assert.Empty(t, result)
	assert.Equal(t, original, storage.record.Factor)
}

func TestEnrollmentStatus(t *testing.T) {
	now := time.Now()
	live := &Waiting{ExpiresAt: now.Add(time.Minute)}
	ended := &Waiting{ExpiresAt: now.Add(-time.Minute)}
	for _, c := range []struct {
		name        string
		factor      *Factor
		waiting     *Waiting
		want        State
		count       int
		wantCount   int
		wantWaiting bool
	}{
		{"off", nil, nil, StateOff, 0, 0, false},
		{"pending", &Factor{LastStep: -1}, nil, StatePending, 0, 0, false},
		{"pending hides codes", &Factor{LastStep: -1}, nil, StatePending, 10, 0, false},
		{"on with all codes", &Factor{ConfirmedAt: &now}, nil, StateOn, 10, 10, false},
		{"on with some codes", &Factor{ConfirmedAt: &now}, nil, StateOn, 3, 3, false},
		{"on without codes", &Factor{ConfirmedAt: &now}, nil, StateOn, 0, 0, false},
		{"on with new codes waiting", &Factor{ConfirmedAt: &now}, live, StateOn, 3, 3, true},
		{"on with new codes that ended", &Factor{ConfirmedAt: &now}, ended, StateOn, 3, 3, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage := &memoryTOTP{record: Record{Factor: c.factor, Waiting: c.waiting}, backupHashes: make([]string, c.count)}
			service := NewService(storage, &fakeSealer{}, func() time.Time { return now })
			status, err := service.Status(t.Context(), 42)
			require.NoError(t, err)
			assert.Equal(t, c.want, status.State)
			assert.Equal(t, c.wantCount, status.BackupCodesLeft)
			assert.Equal(t, c.wantWaiting, status.BackupCodesWaiting)
		})
	}
}

func TestStartEnrollmentFailure(t *testing.T) {
	failure := errors.New("storage or encryption unavailable")
	for _, c := range []struct {
		name    string
		storage *memoryTOTP
		sealer  *fakeSealer
	}{
		{"storage", &memoryTOTP{readErr: failure}, &fakeSealer{}},
		{"encryption", &memoryTOTP{}, &fakeSealer{err: failure}},
	} {
		t.Run(c.name, func(t *testing.T) {
			result, err := NewService(c.storage, c.sealer, time.Now).Start(t.Context(), account.User{ID: 42, Email: "alice@example.com"})
			require.ErrorIs(t, err, failure)
			assert.Empty(t, result, "failed enrollment must not return a secret")
			assert.Nil(t, c.storage.record.Factor)
		})
	}
}

func TestEnrollmentStatusFailure(t *testing.T) {
	failure := errors.New("storage unavailable")
	status, err := NewService(&memoryTOTP{readErr: failure}, &fakeSealer{}, time.Now).Status(t.Context(), 42)
	require.ErrorIs(t, err, failure)
	assert.Empty(t, status)
}

func TestCancelEnrollment(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name    string
		factor  *Factor
		wantErr error
	}{
		{"pending", &Factor{KeyID: "1", Ciphertext: []byte("pending"), LastStep: -1}, nil},
		{"already absent", nil, nil},
		{"confirmed", &Factor{KeyID: "1", Ciphertext: []byte("confirmed"), ConfirmedAt: &now}, ErrAlreadyOn},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage := &memoryTOTP{record: Record{Factor: c.factor}}
			service := NewService(storage, nil, time.Now)

			err := service.Cancel(t.Context(), 42)

			require.ErrorIs(t, err, c.wantErr)
			if c.wantErr != nil {
				assert.Equal(t, c.factor, storage.record.Factor)
				return
			}
			assert.Nil(t, storage.record.Factor)
		})
	}
}

func TestCancelEnrollmentFailure(t *testing.T) {
	failure := errors.New("cancellation storage unavailable")
	original := &Factor{Ciphertext: []byte("pending")}
	storage := &memoryTOTP{record: Record{Factor: original}, readErr: failure}

	err := NewService(storage, nil, time.Now).Cancel(t.Context(), 42)

	require.ErrorIs(t, err, failure)
	assert.Equal(t, original, storage.record.Factor)
}

func TestCancelEnrollmentInvalidUser(t *testing.T) {
	for _, userID := range []int64{0, -1} {
		err := NewService(nil, nil, time.Now).Cancel(t.Context(), userID)
		require.ErrorIs(t, err, account.ErrUserNotFound)
	}
}
