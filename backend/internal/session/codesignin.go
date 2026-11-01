package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"time"

	"inbok/internal/account"
	"inbok/internal/totp"
)

const signInCodeDigits = 6 // RFC 4226 minimum; matches the authenticator settings.

// BackupCode is an owned row that must be consumed with session creation.
type BackupCode struct {
	ID   int64
	Hash string
}

// CodeSignInState is loaded with the user and challenge locked.
type CodeSignInState struct {
	Challenge   Challenge
	Factor      *totp.Factor
	BackupCodes []BackupCode
	Deactivated bool
}

// CodeAction selects a committed counter update, deletion, or session issuance.
type CodeAction int

const (
	CodeActionFail CodeAction = iota
	CodeActionDelete
	CodeActionComplete
)

// CodeSignInChange commits failed-code counts as well as successful sign-ins.
type CodeSignInChange struct {
	Action       CodeAction
	Failures     int
	Factor       *totp.Factor
	Session      State
	Expiry       Expiry
	BackupCodeID int64
	AttemptID    int64
}

type codeSignInStore interface {
	ChallengeUser(ctx context.Context, hash [sha256.Size]byte) (int64, error)
	// CompleteSignIn locks the user before the owned challenge, then calls update.
	// Completion consumes the challenge and optional backup code, saves the step
	// and session, deletes the user's expired sessions, and removes only the
	// successful attempt in one transaction. It reads whether the owner is
	// deactivated.
	CompleteSignIn(ctx context.Context, userID int64, hash [sha256.Size]byte, update func(CodeSignInState) (CodeSignInChange, error)) error
}

// CodeSignIn exchanges password proof and a second factor for a session.
type CodeSignIn struct {
	storage    codeSignInStore
	encryption secretOpener
	verifier   *totp.Verifier
	throttle   codeAttempts
	now        func() time.Time
}

// NewCodeSignIn supplies the same owner-bound encryption and limiter as enrollment.
func NewCodeSignIn(storage codeSignInStore, encryption secretOpener, verifier *totp.Verifier, throttle codeAttempts, now func() time.Time) *CodeSignIn {
	return &CodeSignIn{storage: storage, encryption: encryption, verifier: verifier, throttle: throttle, now: now}
}

// Complete returns a session secret only after consuming both proofs commits.
// The new session keeps the device details of this code request.
// It deletes a deactivated account's challenge and returns
// [account.ErrDeactivated].
func (s *CodeSignIn) Complete(ctx context.Context, secret, code string, device Device) (Issued, error) {
	hash, err := hashSecret(secret)
	if err != nil {
		return Issued{}, ErrInvalidChallenge
	}

	// This lookup only locates the limiter's owner; storage rechecks the proof.
	userID, err := s.storage.ChallengeUser(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return Issued{}, ErrInvalidChallenge
	}
	if err != nil {
		return Issued{}, err
	}
	attemptID, err := s.throttle.Begin(ctx, userID)
	if errors.Is(err, account.ErrUserNotFound) {
		return Issued{}, ErrInvalidChallenge
	}
	if err != nil {
		return Issued{}, err
	}

	var issued Issued
	outcome := ErrInvalidChallenge
	checked := false
	err = s.storage.CompleteSignIn(ctx, userID, hash, func(state CodeSignInState) (CodeSignInChange, error) {
		if state.Challenge.UserID != userID || state.Challenge.TokenHash != hash {
			return CodeSignInChange{}, ErrInvalidChallenge
		}
		// The password step already proved the password, so the account's
		// status may be told; the challenge can never complete.
		if state.Deactivated {
			outcome = account.ErrDeactivated
			return CodeSignInChange{Action: CodeActionDelete}, nil
		}
		if checkChallenge(state.Challenge, s.now) != nil || state.Factor == nil || state.Factor.ConfirmedAt == nil {
			return CodeSignInChange{Action: CodeActionDelete}, nil
		}

		var secret []byte
		if isTOTPCode(code) {
			var err error
			secret, err = s.encryption.Open(state.Factor.KeyID, state.Factor.Ciphertext, "totp:"+strconv.FormatInt(userID, 10))
			if err != nil {
				return CodeSignInChange{}, err
			}
		}

		checked = true
		factor, backupID, err := s.checkCode(state, secret, code)
		if errors.Is(err, totp.ErrInvalidCode) {
			failures := state.Challenge.Failures + 1
			if failures >= challengeFailureLimit {
				return CodeSignInChange{Action: CodeActionDelete}, nil
			}
			// Return the code error after commit so this count is not rolled back.
			outcome = totp.ErrInvalidCode
			return CodeSignInChange{Action: CodeActionFail, Failures: failures}, nil
		}
		if err != nil {
			return CodeSignInChange{}, err
		}

		issued = newSession(userID, device, s.now)
		outcome = nil
		return CodeSignInChange{
			Action: CodeActionComplete, Factor: factor, Session: issued.State, Expiry: expiryAt(s.now()),
			BackupCodeID: backupID, AttemptID: attemptID,
		}, nil
	})
	// A request refused before any code check, e.g. with an expired
	// challenge, made no guess, so it releases its attempt.
	if (err != nil || outcome != nil) && !checked {
		s.throttle.Release(ctx, attemptID)
	}
	if errors.Is(err, ErrNotFound) {
		return Issued{}, ErrInvalidChallenge
	}
	if err != nil {
		return Issued{}, err
	}
	if outcome != nil {
		return Issued{}, outcome
	}
	return issued, nil
}

// checkCode checks code against the factor's decrypted secret when it is six
// digits, or else against the unused backup codes. It returns the factor with
// the accepted step, or the matched backup code's ID.
func (s *CodeSignIn) checkCode(state CodeSignInState, secret []byte, code string) (*totp.Factor, int64, error) {
	if isTOTPCode(code) {
		step, err := s.verifier.Check(string(secret), code, state.Factor.LastStep)
		if err != nil {
			return nil, 0, err
		}
		factor := *state.Factor
		factor.LastStep = step
		return &factor, 0, nil
	}

	// Independently salted hashes require checking each remaining code.
	for _, backup := range state.BackupCodes {
		err := totp.CheckBackupCode(code, backup.Hash)
		if err == nil {
			return nil, backup.ID, nil
		}
		if !errors.Is(err, totp.ErrInvalidCode) {
			return nil, 0, err
		}
	}
	return nil, 0, totp.ErrInvalidCode
}

func isTOTPCode(code string) bool {
	if len(code) != signInCodeDigits {
		return false
	}
	for _, digit := range []byte(code) {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}
