package account

import (
	"context"
	"errors"

	"inbok/internal/emailaddress"
	"inbok/internal/passwordhash"
)

var ErrRecoveryForbidden = errors.New("recovery requires an administrator")

// RecoveryResult contains only counts of removed authentication data.
type RecoveryResult struct {
	Factors        int64
	BackupCodes    int64
	Sessions       int64
	Challenges     int64
	TOTPAttempts   int64
	SignInAttempts int64
}

type recoveryStore interface {
	ReadCredentials(ctx context.Context, email string) (Credentials, error)
	// UpdateRecovery locks the user before calling update. It saves the returned
	// hash, clears authentication data, and records an audit event atomically.
	UpdateRecovery(ctx context.Context, userID int64, update func(User) (string, error)) (RecoveryResult, error)
}

// Recovery restores password access for a host operator, without encryption keys.
type Recovery struct {
	storage recoveryStore
}

func NewRecovery(storage recoveryStore) *Recovery {
	return &Recovery{storage: storage}
}

// Recover replaces an administrator's password and removes old authentication data.
// Administrator access requires fresh TOTP enrollment through the web app.
func (s *Recovery) Recover(ctx context.Context, email, password string) (RecoveryResult, error) {
	user, err := s.Administrator(ctx, email)
	if err != nil {
		return RecoveryResult{}, err
	}

	if err := CheckPassword(password); err != nil {
		return RecoveryResult{}, err
	}

	// Finish Argon2id before acquiring the user lock.
	hash := passwordhash.Hash(password)
	return s.storage.UpdateRecovery(ctx, user.ID, func(user User) (string, error) {
		if user.Role != RoleAdmin {
			return "", ErrRecoveryForbidden
		}
		return hash, nil
	})
}

// Administrator checks the recovery target before the command reads a password.
// Recover repeats the lookup and checks the role again under the storage lock.
// It returns [emailaddress.ErrInvalid] for an invalid email.
func (s *Recovery) Administrator(ctx context.Context, email string) (User, error) {
	email, err := emailaddress.Parse(email)
	if err != nil {
		return User{}, err
	}
	credentials, err := s.storage.ReadCredentials(ctx, email)
	if err != nil {
		return User{}, err
	}
	if credentials.User.Role != RoleAdmin {
		return User{}, ErrRecoveryForbidden
	}
	return credentials.User, nil
}
