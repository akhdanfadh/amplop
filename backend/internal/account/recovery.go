package account

import (
	"context"
	"errors"
	"unicode/utf8"

	"inbok/internal/passwordhash"
)

// Password bounds count Unicode code points, matching HTTP setup validation.
const (
	PasswordMinLength = 15
	PasswordMaxLength = 128
)

var (
	ErrRecoveryForbidden = errors.New("recovery requires an administrator")
	ErrPasswordInvalid   = errors.New("password must contain 15 to 128 characters")
)

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
	credentials, err := s.storage.ReadCredentials(ctx, normalizeEmail(email))
	if err != nil {
		return RecoveryResult{}, err
	}
	if credentials.User.Role != RoleAdmin {
		return RecoveryResult{}, ErrRecoveryForbidden
	}

	length := utf8.RuneCountInString(password)
	if !utf8.ValidString(password) || length < PasswordMinLength || length > PasswordMaxLength {
		return RecoveryResult{}, ErrPasswordInvalid
	}

	// Finish Argon2id before acquiring the user lock.
	hash := passwordhash.Hash(password)
	return s.storage.UpdateRecovery(ctx, credentials.User.ID, func(user User) (string, error) {
		if user.Role != RoleAdmin {
			return "", ErrRecoveryForbidden
		}
		return hash, nil
	})
}
