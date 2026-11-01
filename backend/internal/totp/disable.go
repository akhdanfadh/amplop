package totp

import (
	"context"
	"errors"
	"strconv"

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
	// On successful proof it deletes the factor (cascading to all backup codes)
	// and this owned attempt in one transaction. Sessions remain unchanged.
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
	attemptID, err := s.throttle.Begin(ctx, userID)
	if err != nil {
		return err
	}

	checked := false
	err = s.storage.DisableTOTP(ctx, userID, attemptID, func(state DisableState) error {
		// A stale role from HTTP must not race a promotion into disabling admin TOTP.
		if state.Role != account.RoleMember {
			return ErrDisableForbidden
		}
		if state.Factor == nil || state.Factor.ConfirmedAt == nil {
			return ErrNotOn
		}

		if isTOTPCode(code) {
			secret, err := s.encryption.Open(state.Factor.KeyID, state.Factor.Ciphertext, "totp:"+strconv.FormatInt(userID, 10))
			if err != nil {
				return err
			}

			checked = true
			_, err = s.verifier.Check(string(secret), code, state.Factor.LastStep)
			return err
		}

		checked = true
		// Factor deletion consumes all remaining codes, including this matched proof.
		for _, hash := range state.BackupHashes {
			err := CheckBackupCode(code, hash)
			if err == nil {
				return nil
			}
			if !errors.Is(err, ErrInvalidCode) {
				return err
			}
		}
		return ErrInvalidCode
	})
	// A request refused before any code check, e.g. for an administrator or
	// with TOTP off, made no guess, so it releases its attempt.
	if err != nil && !checked {
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
