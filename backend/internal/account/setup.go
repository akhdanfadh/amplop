package account

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"

	"amplop/internal/emailaddress"
	"amplop/internal/passwordhash"
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

// SetupInput carries the token and credentials for [Service.Setup].
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

// Setup claims the first administrator without creating a session and returns
// it. It returns [emailaddress.ErrInvalid] for an invalid email, and
// [ErrSetupDone], [ErrSetupUnconfigured], or [ErrSetupTokenInvalid] when setup
// is not open to input's token.
func (s *Service) Setup(ctx context.Context, input SetupInput) (User, error) {
	// 1. Refuse an invalid email.
	email, err := emailaddress.Parse(input.Email)
	if err != nil {
		return User{}, err
	}

	// 2. Refuse when setup is done or not configured.
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

	// 3. Compare the token before the password hash. Equal-size digests
	// avoid revealing the configured token's length.
	token := sha256.Sum256([]byte(input.Token))
	if subtle.ConstantTimeCompare(token[:], s.tokenHash[:]) != 1 {
		return User{}, ErrSetupTokenInvalid
	}

	// 4. Hash the password: Argon2id must finish before storage takes the
	// table lock.
	passwordHash, err := passwordhash.Hash(ctx, input.Password)
	if err != nil {
		return User{}, err
	}

	// 5. Under the lock, insert the administrator unless one exists.
	admin := NewAdmin{Email: email, PasswordHash: passwordHash}
	return s.storage.UpdateSetup(ctx, func(state SetupState) (NewAdmin, error) {
		if state.AdminExists {
			return NewAdmin{}, ErrSetupDone
		}
		return admin, nil
	})
}
