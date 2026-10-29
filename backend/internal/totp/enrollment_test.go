package totp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
)

type memoryFactors struct {
	factor          *Factor
	backupCodesLeft int
	err             error
}

func (m *memoryFactors) ReadTOTPStatus(context.Context, int64) (*StatusRecord, error) {
	if m.err != nil || m.factor == nil {
		return nil, m.err
	}
	return &StatusRecord{ConfirmedAt: m.factor.ConfirmedAt, BackupCodesLeft: m.backupCodesLeft}, nil
}
func (m *memoryFactors) UpdateTOTP(_ context.Context, _ int64, update func(*Factor) (Factor, error)) error {
	if m.err != nil {
		return m.err
	}
	next, err := update(m.factor)
	if err != nil {
		return err
	}
	m.factor = &next
	return nil
}

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
	storage, sealer := &memoryFactors{}, &fakeSealer{}
	service := NewService(storage, sealer)
	user := account.User{ID: 42, Email: "alice@example.com", Role: account.RoleMember}

	first, err := service.Start(t.Context(), user)
	require.NoError(t, err)
	require.NotNil(t, storage.factor)
	assert.NotEmpty(t, first.Secret)
	assert.Contains(t, first.URI, "otpauth://totp/")
	assert.Equal(t, []byte(first.Secret), sealer.plaintext)
	assert.Equal(t, "totp:42", sealer.owner)
	assert.Equal(t, "1", storage.factor.KeyID)
	assert.Equal(t, []byte("encrypted"), storage.factor.Ciphertext)
	assert.Nil(t, storage.factor.ConfirmedAt)
	assert.Equal(t, int64(-1), storage.factor.LastStep)

	second, err := service.Start(t.Context(), user)
	require.NoError(t, err)
	assert.NotEqual(t, first.Secret, second.Secret, "restart replaces the pending secret")
	assert.Equal(t, []byte(second.Secret), sealer.plaintext)
}

func TestStartEnrollmentAlreadyOn(t *testing.T) {
	now := time.Now()
	original := &Factor{KeyID: "1", Ciphertext: []byte("original"), ConfirmedAt: &now, LastStep: 12}
	storage := &memoryFactors{factor: original}
	service := NewService(storage, &fakeSealer{})

	result, err := service.Start(t.Context(), account.User{ID: 42, Email: "alice@example.com"})
	require.ErrorIs(t, err, ErrAlreadyOn)
	assert.Empty(t, result)
	assert.Equal(t, original, storage.factor)
}

func TestEnrollmentStatus(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name      string
		factor    *Factor
		want      State
		count     int
		wantCount int
	}{
		{"off", nil, StateOff, 0, 0},
		{"pending", &Factor{LastStep: -1}, StatePending, 0, 0},
		{"pending hides codes", &Factor{LastStep: -1}, StatePending, 10, 0},
		{"on with all codes", &Factor{ConfirmedAt: &now}, StateOn, 10, 10},
		{"on with some codes", &Factor{ConfirmedAt: &now}, StateOn, 3, 3},
		{"on without codes", &Factor{ConfirmedAt: &now}, StateOn, 0, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := NewService(&memoryFactors{factor: c.factor, backupCodesLeft: c.count}, &fakeSealer{})
			status, err := service.Status(t.Context(), 42)
			require.NoError(t, err)
			assert.Equal(t, c.want, status.State)
			assert.Equal(t, c.wantCount, status.BackupCodesLeft)
		})
	}
}

func TestStartEnrollmentFailure(t *testing.T) {
	failure := errors.New("storage or encryption unavailable")
	for _, c := range []struct {
		name    string
		storage *memoryFactors
		sealer  *fakeSealer
	}{
		{"storage", &memoryFactors{err: failure}, &fakeSealer{}},
		{"encryption", &memoryFactors{}, &fakeSealer{err: failure}},
	} {
		t.Run(c.name, func(t *testing.T) {
			result, err := NewService(c.storage, c.sealer).Start(t.Context(), account.User{ID: 42, Email: "alice@example.com"})
			require.ErrorIs(t, err, failure)
			assert.Empty(t, result, "failed enrollment must not return a secret")
			assert.Nil(t, c.storage.factor)
		})
	}
}

func TestEnrollmentStatusFailure(t *testing.T) {
	failure := errors.New("storage unavailable")
	status, err := NewService(&memoryFactors{err: failure}, &fakeSealer{}).Status(t.Context(), 42)
	require.ErrorIs(t, err, failure)
	assert.Empty(t, status)
}
