package totp

import (
	"context"
	"errors"
	"strconv"
	"time"

	"inbok/internal/account"
)

// State identifies whether a factor is absent, awaiting proof, or confirmed.
type State string

const (
	StateOff     State = "off"
	StatePending State = "pending"
	StateOn      State = "on"
)

// ErrAlreadyOn prevents replacing a confirmed authenticator through enrollment.
var ErrAlreadyOn = errors.New("TOTP is already on")

// ErrNotPending means confirmation has no pending factor to activate.
var ErrNotPending = errors.New("TOTP enrollment is not pending")

// Factor is the encrypted factor and the last accepted step saved by storage.
type Factor struct {
	KeyID       string
	Ciphertext  []byte
	ConfirmedAt *time.Time
	LastStep    int64
}

// Storage serializes factor changes, including when a user has no factor yet.
type Storage interface {
	ReadTOTP(ctx context.Context, userID int64) (*Factor, error)

	// UpdateTOTP locks the user, loads the factor (nil if absent), calls update,
	// and saves its result in one transaction. An error rolls back the write.
	UpdateTOTP(ctx context.Context, userID int64, update func(*Factor) (Factor, error)) error
}

type sealer interface {
	Seal(plaintext []byte, owner string) (string, []byte, error)
}

// Service manages authenticator enrollment and settings.
type Service struct {
	storage    Storage
	encryption sealer
}

// NewService supplies storage and owner-bound encryption for saved secrets.
func NewService(storage Storage, encryption sealer) *Service {
	return &Service{storage: storage, encryption: encryption}
}

// Started is the enrollment data shown to the user after storage commits.
type Started struct {
	Secret string
	URI    string
}

// Status contains only display data, never the stored secret.
type Status struct {
	State           State
	BackupCodesLeft int
}

// Start replaces a pending secret but cannot replace a confirmed factor.
func (s *Service) Start(ctx context.Context, user account.User) (Started, error) {
	value, err := newEnrollment(user.Email)
	if err != nil {
		return Started{}, err
	}
	keyID, ciphertext, err := s.encryption.Seal([]byte(value.secret), "totp:"+strconv.FormatInt(user.ID, 10))
	if err != nil {
		return Started{}, err
	}

	err = s.storage.UpdateTOTP(ctx, user.ID, func(current *Factor) (Factor, error) {
		if current != nil && current.ConfirmedAt != nil {
			return Factor{}, ErrAlreadyOn
		}
		return Factor{KeyID: keyID, Ciphertext: ciphertext, LastStep: -1}, nil
	})
	if err != nil {
		return Started{}, err
	}
	return Started{Secret: value.secret, URI: value.uri}, nil
}

// Status reports off for a missing factor; generation alone leaves it pending.
func (s *Service) Status(ctx context.Context, userID int64) (Status, error) {
	factor, err := s.storage.ReadTOTP(ctx, userID)
	if err != nil {
		return Status{}, err
	}
	if factor == nil {
		return Status{State: StateOff}, nil
	}
	if factor.ConfirmedAt == nil {
		return Status{State: StatePending}, nil
	}
	return Status{State: StateOn}, nil
}
