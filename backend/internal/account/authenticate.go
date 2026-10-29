package account

import (
	"context"
	"errors"

	"inbok/internal/passwordhash"
)

// dummyPasswordHash is the fixed Argon2id hash checked for unknown accounts.
// Its work parameters match new passwords; the input is not a secret.
const dummyPasswordHash = "$argon2id$v=19$m=19456,t=2,p=1$MDEyMzQ1Njc4OWFiY2RlZg$DCD4ERxMEHwAyfsOVs7QftC+Pw565z7RS2Cs1YOL4dY"

var (
	// ErrUserNotFound means no account matches the storage lookup.
	ErrUserNotFound = errors.New("user not found")
	// ErrInvalidCredentials means the email or password did not match.
	ErrInvalidCredentials = errors.New("invalid credentials")
)

// Credentials is a stored account and its password hash, used for verification.
type Credentials struct {
	User         User
	PasswordHash string
}

// Authenticate verifies credentials and returns the account on success.
// Unknown accounts do the same password work and receive the same error as
// wrong passwords. Callers must reserve a throttle attempt before calling it.
func (s *Service) Authenticate(ctx context.Context, email, password string) (User, error) {
	credentials, err := s.storage.ReadCredentials(ctx, normalizeEmail(email))
	missing := errors.Is(err, ErrUserNotFound)
	if err != nil && !missing {
		return User{}, err
	}
	if missing {
		credentials.PasswordHash = dummyPasswordHash
	}

	match, err := passwordhash.Verify(password, credentials.PasswordHash)
	if err != nil {
		return User{}, err
	}
	if missing || !match {
		return User{}, ErrInvalidCredentials
	}
	return credentials.User, nil
}
