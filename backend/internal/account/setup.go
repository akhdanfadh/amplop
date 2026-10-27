package account

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
)

// SetupStatus describes whether the first administrator can be created.
type SetupStatus string

const (
	SetupUnconfigured SetupStatus = "unconfigured"
	SetupOpen         SetupStatus = "open"
	SetupDone         SetupStatus = "done"
)

// Setup errors identify expected failures without including credentials.
var (
	ErrSetupDone         = errors.New("setup is done")
	ErrSetupUnconfigured = errors.New("setup is unconfigured")
	ErrSetupTokenInvalid = errors.New("setup token is invalid")
)

// SetupInput carries the token and credentials after request validation.
type SetupInput struct {
	Token    string
	Email    string
	Password string
}

// NewAdmin is the normalized email and password hash to save.
type NewAdmin struct {
	Email        string
	PasswordHash string
}

// SetupState is the saved state needed to decide whether setup is done.
type SetupState struct {
	AdminExists bool
}

// SetupStatus reports done before checking whether the token is still configured.
func (s *Service) SetupStatus(ctx context.Context) (SetupStatus, error) {
	state, err := s.storage.ReadSetup(ctx)
	if err != nil {
		return "", err
	}
	if state.AdminExists {
		return SetupDone, nil
	}
	if s.tokenHash == nil {
		return SetupUnconfigured, nil
	}
	return SetupOpen, nil
}

// Setup claims the first administrator without creating a session.
func (s *Service) Setup(ctx context.Context, input SetupInput) (User, error) {
	status, err := s.SetupStatus(ctx)
	if err != nil {
		return User{}, err
	}
	switch status {
	case SetupDone:
		return User{}, ErrSetupDone
	case SetupUnconfigured:
		return User{}, ErrSetupUnconfigured
	}

	// Equal-size digests avoid revealing the configured token's length.
	token := sha256.Sum256([]byte(input.Token))
	if subtle.ConstantTimeCompare(token[:], s.tokenHash[:]) != 1 {
		return User{}, ErrSetupTokenInvalid
	}

	// Argon2id must finish before storage takes the table lock.
	admin := NewAdmin{
		Email:        strings.ToLower(strings.TrimSpace(input.Email)),
		PasswordHash: hashPassword(input.Password),
	}
	return s.storage.UpdateSetup(ctx, func(state SetupState) (NewAdmin, error) {
		if state.AdminExists {
			return NewAdmin{}, ErrSetupDone
		}
		return admin, nil
	})
}
