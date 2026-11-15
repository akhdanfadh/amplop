package totp

import (
	"context"
	"errors"

	"inbok/internal/account"
)

// ErrDisableForbidden keeps TOTP mandatory for administrators and unknown roles.
var ErrDisableForbidden = errors.New("TOTP cannot be disabled for this role")

// DisableState is loaded with the owner locked, including the current role.
type DisableState struct {
	Role         account.Role
	Factor       *Factor
	BackupHashes []string
}

type disableStore interface {
	// DisableTOTP locks the user and loads role, factor, and remaining backup hashes.
	// On successful proof it deletes the factor (cascading to all backup codes,
	// active and waiting) and this owned attempt in one transaction. Sessions
	// remain unchanged.
	// A missing or deactivated user returns [account.ErrUserNotFound].
	DisableTOTP(ctx context.Context, userID, attemptID int64, check func(DisableState) error) error
}

// Disabler removes an optional factor after checking current second-factor proof.
type Disabler struct {
	storage    disableStore
	encryption opener
	verifier   *Verifier
	throttle   codeAttempts
}

func NewDisabler(storage disableStore, encryption opener, verifier *Verifier, throttle codeAttempts) *Disabler {
	return &Disabler{storage: storage, encryption: encryption, verifier: verifier, throttle: throttle}
}

// Disable requires a checked user's ID; its role is rechecked inside storage.
func (s *Disabler) Disable(ctx context.Context, userID int64, code string) error {
	// 1. Reserve an attempt before any code is verified.
	attemptID, err := s.throttle.Begin(ctx, userID)
	if err != nil {
		return err
	}

	verified := false
	err = s.storage.DisableTOTP(ctx, userID, attemptID, func(state DisableState) error {
		// 2. Under the owner's lock, refuse before verifying anything. A
		// stale role from HTTP must not race a promotion into disabling
		// admin TOTP.
		if state.Role != account.RoleMember {
			return ErrDisableForbidden
		}
		if state.Factor == nil || state.Factor.ConfirmedAt == nil {
			return ErrNotOn
		}

		// 3. Verify the code: a TOTP code against the decrypted secret, or
		// else a backup code.
		if isTOTPCode(code) {
			secret, err := s.encryption.Open(state.Factor.KeyID, state.Factor.Ciphertext, secretOwner(userID))
			if err != nil {
				return err
			}

			verified = true
			_, err = s.verifier.Verify(string(secret), code, state.Factor.LastStep)
			return err
		}

		verified = true
		// Factor deletion consumes all remaining codes, including this matched proof.
		_, err := MatchBackupCode(ctx, code, state.BackupHashes)
		// A request that ended while waiting to compare made no guess.
		if errors.Is(err, ErrNotVerified) {
			verified = false
		}
		return err
	})

	// 4. Release the attempt when no code was verified. A request refused
	// before that, e.g. for an administrator or with TOTP off, made no guess.
	if err != nil && !verified {
		s.throttle.Release(ctx, attemptID)
	}
	return err
}

func isTOTPCode(code string) bool {
	if len(code) != codeDigits.Length() {
		return false
	}
	for _, digit := range []byte(code) {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}
