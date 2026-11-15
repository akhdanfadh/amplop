package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"time"

	"inbok/internal/account"
	"inbok/internal/randomsecret"
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

type secretOpener interface {
	Open(keyID string, ciphertext []byte, owner string) ([]byte, error)
}

type codeAttempts interface {
	Begin(ctx context.Context, userID int64) (int64, error)
	Release(ctx context.Context, id int64)
}

// CodeSignIn exchanges password proof and a second factor for a session.
type CodeSignIn struct {
	storage    codeSignInStore
	encryption secretOpener
	verifier   *totp.Verifier
	throttle   codeAttempts
	now        func() time.Time
}

// NewCodeSignIn supplies owner-bound encryption, the code verifier and
// attempt limit, and a clock.
func NewCodeSignIn(storage codeSignInStore, encryption secretOpener, verifier *totp.Verifier, throttle codeAttempts, now func() time.Time) *CodeSignIn {
	return &CodeSignIn{storage: storage, encryption: encryption, verifier: verifier, throttle: throttle, now: now}
}

// Complete returns a session secret only after consuming both proofs commits.
// The new session keeps the device details of this code request.
// It deletes a deactivated account's challenge and returns
// [account.ErrDeactivated].
func (s *CodeSignIn) Complete(ctx context.Context, secret, code string, device Device) (Issued, error) {
	// 1. Refuse a malformed secret before any storage.
	hash, err := randomsecret.Hash(secret)
	if err != nil {
		return Issued{}, ErrInvalidChallenge
	}

	// 2. Find the challenge's user. This lookup only locates the limiter's
	// owner; storage checks the challenge again under its lock.
	userID, err := s.storage.ChallengeUser(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return Issued{}, ErrInvalidChallenge
	}
	if err != nil {
		return Issued{}, err
	}

	// 3. Reserve an attempt before any code is verified.
	attemptID, err := s.throttle.Begin(ctx, userID)
	if errors.Is(err, account.ErrUserNotFound) {
		return Issued{}, ErrInvalidChallenge
	}
	if err != nil {
		return Issued{}, err
	}

	var issued Issued
	outcome := ErrInvalidChallenge
	verified := false
	err = s.storage.CompleteSignIn(ctx, userID, hash, func(state CodeSignInState) (CodeSignInChange, error) {
		// 4. Under the locks, refuse a challenge that cannot complete before
		// verifying anything.
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

		// 5. Decrypt the factor's secret, for a TOTP code only.
		var secret []byte
		if isTOTPCode(code) {
			var err error
			secret, err = s.encryption.Open(state.Factor.KeyID, state.Factor.Ciphertext, "totp:"+strconv.FormatInt(userID, 10))
			if err != nil {
				return CodeSignInChange{}, err
			}
		}

		// 6. Verify the code. A wrong code counts against the challenge, and
		// the last allowed failure deletes it.
		verified = true
		factor, backupID, err := s.verifyCode(ctx, state, secret, code)
		// A request that ended while waiting to compare made no guess.
		if errors.Is(err, totp.ErrNotVerified) {
			verified = false
		}
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

		// 7. Issue the session.
		issued = newSession(userID, device, s.now)
		outcome = nil
		return CodeSignInChange{
			Action: CodeActionComplete, Factor: factor, Session: issued.State, Expiry: expiryAt(s.now()),
			BackupCodeID: backupID, AttemptID: attemptID,
		}, nil
	})

	// 8. Release the attempt when no code was verified. A request refused
	// before that, e.g. with an expired challenge, made no guess.
	if (err != nil || outcome != nil) && !verified {
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

// verifyCode verifies code against the factor's decrypted secret when it is six
// digits, or else against the unused backup codes. It returns the factor with
// the accepted step, or the matched backup code's ID.
func (s *CodeSignIn) verifyCode(ctx context.Context, state CodeSignInState, secret []byte, code string) (*totp.Factor, int64, error) {
	if isTOTPCode(code) {
		step, err := s.verifier.Verify(string(secret), code, state.Factor.LastStep)
		if err != nil {
			return nil, 0, err
		}
		factor := *state.Factor
		factor.LastStep = step
		return &factor, 0, nil
	}

	hashes := make([]string, len(state.BackupCodes))
	for i, backup := range state.BackupCodes {
		hashes[i] = backup.Hash
	}
	i, err := totp.MatchBackupCode(ctx, code, hashes)
	if err != nil {
		return nil, 0, err
	}
	return nil, state.BackupCodes[i].ID, nil
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
