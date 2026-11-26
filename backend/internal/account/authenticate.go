package account

import (
	"context"
	"errors"

	"amplop/internal/emailaddress"
	"amplop/internal/passwordhash"
)

// dummyPasswordHash is the fixed Argon2id hash checked for unknown accounts.
// Its work parameters match new passwords; the input is not a secret.
const dummyPasswordHash = "$argon2id$v=19$m=19456,t=2,p=1$MDEyMzQ1Njc4OWFiY2RlZg$UtGYoBi14hmM9+fDYjSJeHwxppuVkz5WIA1Av0p04Hk"

var (
	// ErrUserNotFound means no account matches the storage lookup. Writes made
	// for a signed-in user also return it once the account is deactivated.
	ErrUserNotFound = errors.New("user not found")
	// ErrInvalidCredentials means the email or password did not match.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrDeactivated means the password was right but an administrator has
	// deactivated the account.
	ErrDeactivated = errors.New("account is deactivated")
)

// Credentials is a stored account and its password hash, used for verification.
type Credentials struct {
	User         User
	PasswordHash string
}

// Authenticate returns the verified credentials so sign-in can detect a
// password replacement before issuing a session or challenge.
// Unknown accounts do the same password work and receive the same error as
// wrong passwords. It returns [emailaddress.ErrInvalid] for an invalid email.
// Callers must reserve a throttle attempt before calling it.
func (s *Service) Authenticate(ctx context.Context, email, password string) (Credentials, error) {
	// 1. Refuse an invalid email before any password work.
	email, err := emailaddress.Parse(email)
	if err != nil {
		return Credentials{}, err
	}

	// 2. Read the credentials. An unknown account gets a dummy hash, so it
	// costs the same password work as a known one.
	credentials, err := s.storage.ReadCredentials(ctx, email)
	missing := errors.Is(err, ErrUserNotFound)
	if err != nil && !missing {
		return Credentials{}, err
	}
	if missing {
		credentials.PasswordHash = dummyPasswordHash
	}

	// 3. Verify the password.
	match, err := passwordhash.Verify(ctx, password, credentials.PasswordHash)
	if err != nil {
		return Credentials{}, err
	}
	if missing || !match {
		return Credentials{}, ErrInvalidCredentials
	}
	return credentials, nil
}
