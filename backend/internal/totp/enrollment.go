package totp

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"time"

	"inbok/internal/account"
	"inbok/internal/randomsecret"
)

// State identifies whether a factor is absent, awaiting proof, or confirmed.
type State string

const (
	StateOff     State = "off"
	StatePending State = "pending"
	StateOn      State = "on"
)

var (
	// ErrAlreadyOn prevents replacing a confirmed authenticator through enrollment.
	ErrAlreadyOn = errors.New("TOTP is already on")

	// ErrNotPending means confirmation has no pending factor to activate.
	ErrNotPending = errors.New("TOTP enrollment is not pending")

	// ErrNotWaiting means no live backup-code set waits for this token.
	ErrNotWaiting = errors.New("no backup codes waiting")
)

// Factor is the encrypted factor and the last accepted step saved by storage.
type Factor struct {
	KeyID       string
	Ciphertext  []byte
	ConfirmedAt *time.Time
	LastStep    int64
}

// Waiting is a backup-code set that waits for the person to say they saved
// it: the hashes that become active, and the hash of the one-time token that
// activates it.
type Waiting struct {
	Hashes    []string
	TokenHash [sha256.Size]byte
	ExpiresAt time.Time
}

// checkWaiting returns [ErrNotWaiting] unless waiting is present and not
// expired at now.
func checkWaiting(waiting *Waiting, now time.Time) error {
	if waiting == nil || !now.Before(waiting.ExpiresAt) {
		return ErrNotWaiting
	}
	return nil
}

// VerifyWaiting returns [ErrNotWaiting] unless waiting is present, not
// expired at now, and token is its one-time token.
func VerifyWaiting(waiting *Waiting, token string, now time.Time) error {
	if err := checkWaiting(waiting, now); err != nil {
		return err
	}
	hash, err := randomsecret.Hash(token)
	if err != nil || hash != waiting.TokenHash {
		return ErrNotWaiting
	}
	return nil
}

// Record is a user's factor and waiting set, loaded with the owner locked.
// Either can be nil.
type Record struct {
	Factor  *Factor
	Waiting *Waiting
}

// WaitingWrite is what a [Change] does to the waiting backup-code set.
type WaitingWrite int

const (
	// KeepWaiting leaves the waiting set as it is.
	KeepWaiting WaitingWrite = iota
	// DeleteWaiting deletes the waiting set.
	DeleteWaiting
	// SaveWaiting saves [Change.Waiting], replacing any waiting set.
	SaveWaiting
)

// Change is one write to a user's factor. Storage applies, in this order,
// each field that is set, all in one transaction.
type Change struct {
	// Factor, when set, saves the factor.
	Factor *Factor
	// WaitingWrite keeps, deletes, or saves the waiting set; Waiting is the
	// set [SaveWaiting] saves and is nil otherwise.
	WaitingWrite WaitingWrite
	Waiting      *Waiting
	// BackupHashes, when not nil, replaces every active backup code.
	BackupHashes []string
	// AttemptID, when not zero, removes the owner's successful attempt.
	AttemptID int64
}

// Storage serializes factor changes, including when a user has no factor yet.
type Storage interface {
	// ReadTOTPStatus returns metadata and count from one snapshot, nil if absent.
	ReadTOTPStatus(ctx context.Context, userID int64) (*StatusRecord, error)

	// UpdateTOTP locks the user, loads the factor and waiting set, calls
	// update, and saves its [Change] in one transaction. An error rolls back
	// every write. A missing or deactivated user returns
	// [account.ErrUserNotFound].
	UpdateTOTP(ctx context.Context, userID int64, update func(Record) (Change, error)) error

	// CancelTOTP locks the owner, checks the current factor, and deletes it,
	// with its waiting set, in one transaction. A missing factor is passed to
	// check as nil. A missing or deactivated user returns
	// [account.ErrUserNotFound].
	CancelTOTP(ctx context.Context, userID int64, check func(*Factor) error) error
}

type sealer interface {
	Seal(plaintext []byte, owner string) (string, []byte, error)
}

// Service manages authenticator enrollment and status.
type Service struct {
	storage    Storage
	encryption sealer
	now        func() time.Time
}

// NewService supplies storage, owner-bound encryption for saved secrets, and
// a clock.
func NewService(storage Storage, encryption sealer, now func() time.Time) *Service {
	return &Service{storage: storage, encryption: encryption, now: now}
}

// Started is the enrollment data shown to the user after storage commits.
type Started struct {
	Secret string
	URI    string
}

// Status contains only display data, never the stored secret.
type Status struct {
	State              State
	BackupCodesLeft    int
	BackupCodesWaiting bool
}

// StatusRecord contains stored display metadata, without secrets or hashes.
// WaitingExpiresAt is nil when no set waits.
type StatusRecord struct {
	ConfirmedAt      *time.Time
	BackupCodesLeft  int
	WaitingExpiresAt *time.Time
}

// Start replaces a pending secret but cannot replace a confirmed factor.
func (s *Service) Start(ctx context.Context, user account.User) (Started, error) {
	// 1. Create and seal the secret before storage takes its lock.
	value, err := newEnrollment(user.Email)
	if err != nil {
		return Started{}, err
	}
	keyID, ciphertext, err := s.encryption.Seal([]byte(value.secret), secretOwner(user.ID))
	if err != nil {
		return Started{}, err
	}

	// 2. Under the owner's lock, save it unless a confirmed factor exists.
	err = s.storage.UpdateTOTP(ctx, user.ID, func(current Record) (Change, error) {
		if current.Factor != nil && current.Factor.ConfirmedAt != nil {
			return Change{}, ErrAlreadyOn
		}
		factor := Factor{KeyID: keyID, Ciphertext: ciphertext, LastStep: -1}
		return Change{Factor: &factor}, nil
	})
	if err != nil {
		return Started{}, err
	}
	return Started{Secret: value.secret, URI: value.uri}, nil
}

// Codes is a waiting backup-code set to show, and the one-time token that
// activates it.
type Codes struct {
	Codes []string
	Token string
}

// Cancel discards pending enrollment. Repeating a completed cancellation is safe.
func (s *Service) Cancel(ctx context.Context, userID int64) error {
	// 1. Refuse an invalid user ID before storage.
	if userID <= 0 {
		return account.ErrUserNotFound
	}

	// 2. Delete the pending factor. Check it under the owner lock so
	// confirmation cannot race this deletion.
	return s.storage.CancelTOTP(ctx, userID, func(current *Factor) error {
		if current != nil && current.ConfirmedAt != nil {
			return ErrAlreadyOn
		}
		return nil
	})
}

// Status reports off for a missing factor; generation alone leaves it
// pending. While it is on, it says whether a live backup-code set waits to
// be saved.
func (s *Service) Status(ctx context.Context, userID int64) (Status, error) {
	record, err := s.storage.ReadTOTPStatus(ctx, userID)
	if err != nil {
		return Status{}, err
	}
	if record == nil {
		return Status{State: StateOff}, nil
	}
	if record.ConfirmedAt == nil {
		return Status{State: StatePending}, nil
	}
	waiting := record.WaitingExpiresAt != nil && s.now().Before(*record.WaitingExpiresAt)
	return Status{State: StateOn, BackupCodesLeft: record.BackupCodesLeft, BackupCodesWaiting: waiting}, nil
}

// secretOwner binds an encrypted authenticator secret to its user.
func secretOwner(userID int64) string {
	return "totp:" + strconv.FormatInt(userID, 10)
}
