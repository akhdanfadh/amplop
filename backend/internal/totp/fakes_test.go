package totp

import (
	"bytes"
	"context"
	"errors"
	"net/netip"

	"amplop/internal/account"
)

// memoryTOTP is [Storage] in memory. It applies a [Change] the way
// PostgreSQL does, and records what it was asked to do.
type memoryTOTP struct {
	record             Record
	backupHashes       []string
	clearedAttempts    []int64
	readErr, commitErr error
	calls              int
	userID             int64
}

func (m *memoryTOTP) ReadTOTPStatus(context.Context, int64) (*StatusRecord, error) {
	if m.readErr != nil || m.record.Factor == nil {
		return nil, m.readErr
	}
	status := &StatusRecord{
		ConfirmedAt: m.record.Factor.ConfirmedAt, StartedAt: m.record.Factor.StartedAt,
		BackupCodesLeft: len(m.backupHashes),
	}
	if m.record.Waiting != nil {
		expiresAt := m.record.Waiting.ExpiresAt
		status.WaitingExpiresAt = &expiresAt
	}
	return status, nil
}

func (m *memoryTOTP) UpdateTOTP(_ context.Context, userID int64, update func(Record) (Change, error)) error {
	m.calls++
	m.userID = userID
	if m.readErr != nil {
		return m.readErr
	}
	next, err := update(m.record)
	if err != nil {
		return err
	}
	if m.commitErr != nil {
		return m.commitErr
	}
	if next.Factor != nil {
		m.record.Factor = next.Factor
	}
	switch next.WaitingWrite {
	case DeleteWaiting:
		m.record.Waiting = nil
	case SaveWaiting:
		m.record.Waiting = next.Waiting
	}
	if next.BackupHashes != nil {
		m.backupHashes = next.BackupHashes
	}
	if next.AttemptID != 0 {
		m.clearedAttempts = append(m.clearedAttempts, next.AttemptID)
	}
	return nil
}

func (m *memoryTOTP) CancelTOTP(_ context.Context, _ int64, check func(*Factor) error) error {
	if m.readErr != nil {
		return m.readErr
	}
	if err := check(m.record.Factor); err != nil {
		return err
	}
	m.record = Record{}
	return nil
}

// fakeCipher seals by prefixing the owner, so opening checks the owner as
// the real keyring does.
type fakeCipher struct {
	sealErr, openErr error
	sealed           []string
	opens            int
}

func (f *fakeCipher) Seal(plaintext []byte, owner string) (string, []byte, error) {
	f.sealed = append(f.sealed, owner)
	return "1", append([]byte(owner+"|"), plaintext...), f.sealErr
}

func (f *fakeCipher) Open(_ string, ciphertext []byte, owner string) ([]byte, error) {
	f.opens++
	if f.openErr != nil {
		return nil, f.openErr
	}
	plaintext, ok := bytes.CutPrefix(ciphertext, []byte(owner+"|"))
	if !ok {
		return nil, errors.New("wrong owner")
	}
	return plaintext, nil
}

// sealedSecret is vectorSecret as [fakeCipher] seals it for user 42.
var sealedSecret = []byte("totp:42|" + vectorSecret)

// fakePasswords accepts only password "right".
type fakePasswords struct {
	err   error
	calls int
}

func (f *fakePasswords) VerifyPassword(_ context.Context, _ account.User, password string, _ netip.Addr) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	if password != "right" {
		return account.ErrInvalidCredentials
	}
	return nil
}

type backupOpener struct {
	err   error
	owner string
	calls int
}

func (f *backupOpener) Open(_ string, _ []byte, owner string) ([]byte, error) {
	f.calls++
	f.owner = owner
	return []byte(vectorSecret), f.err
}

type backupAttempts struct {
	calls    int
	err      error
	released []int64
}

func (f *backupAttempts) Begin(context.Context, int64) (int64, error) {
	f.calls++
	return 7, f.err
}

func (f *backupAttempts) Release(_ context.Context, id int64) {
	f.released = append(f.released, id)
}
