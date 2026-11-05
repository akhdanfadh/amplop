package account

import (
	"context"
	"errors"

	"inbok/internal/emailaddress"
	"inbok/internal/passwordhash"
	"inbok/internal/systememail"
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
	ResetLinks     int64 // password reset links, deleted so none can undo the recovery
}

type recoveryStore interface {
	ReadCredentials(ctx context.Context, email string) (Credentials, error)

	// UpdateRecovery locks the user before calling update. It saves the
	// returned hash, clears authentication data, records an audit event, and
	// saves the recovery notice for the worker, atomically.
	UpdateRecovery(ctx context.Context, userID int64, update func(User) (string, error)) (RecoveryResult, error)

	// ReadSystemEmail returns the saved system email settings, nil when
	// none are saved.
	ReadSystemEmail(ctx context.Context) (*systememail.Record, error)
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
	// 1. Refuse a target that is not an administrator.
	user, err := s.Administrator(ctx, email)
	if err != nil {
		return RecoveryResult{}, err
	}

	// 2. Refuse a password that breaks the rule.
	if err := ValidatePassword(password); err != nil {
		return RecoveryResult{}, err
	}

	// 3. Finish Argon2id before acquiring the user lock.
	hash, err := passwordhash.Hash(ctx, password)
	if err != nil {
		return RecoveryResult{}, err
	}

	// 4. Under the user's lock, check the role again and replace the
	// password.
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

// NoticeBlocker returns why system email cannot send the recovery notice
// now, [NoticeNotConfigured] or [NoticeNotTested], or "" when it can. It
// reads no keys, so the command can tell the operator before the worker
// tries. It returns storage's error.
func (s *Recovery) NoticeBlocker(ctx context.Context) (NoticeOutcome, error) {
	settings, err := s.storage.ReadSystemEmail(ctx)
	switch {
	case err != nil:
		return "", err
	case settings == nil:
		return NoticeNotConfigured, nil
	case settings.TestedAt == nil:
		return NoticeNotTested, nil
	}
	return "", nil
}
