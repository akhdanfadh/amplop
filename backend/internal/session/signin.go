package session

import (
	"context"
	"errors"
	"time"

	"inbok/internal/account"
	"inbok/internal/emailaddress"
	"inbok/internal/totp"
)

// SignInStep identifies whether password proof completes sign-in.
type SignInStep string

const (
	SignInStepComplete SignInStep = "complete"
	SignInStepTOTP     SignInStep = "totp"
)

// SignedIn contains only the cookie to issue after its storage write commits.
type SignedIn struct {
	Step      SignInStep
	Secret    string
	ExpiresAt time.Time
}

// SignInState is the current password hash, factor, and status, read with
// the user locked.
type SignInState struct {
	PasswordHash string
	Factor       *totp.Factor
	Deactivated  bool
}

// SignInChange saves exactly one of Session or Challenge, selected by Step.
type SignInChange struct {
	Step      SignInStep
	Session   State
	Expiry    Expiry
	Challenge Challenge
	Cutoff    time.Time
}

// accountService verifies password credentials.
type accountService interface {
	Authenticate(ctx context.Context, email, password string) (account.Credentials, error)
}

// Service manages browser sessions and resolves their current identity.
type Service struct {
	storage  Storage
	accounts accountService
	throttle *Throttle
	now      func() time.Time
}

// NewService creates a session service using storage, accounts, and a limiter.
// The clock determines when new sessions start and expire.
func NewService(storage Storage, accounts accountService, throttle *Throttle, now func() time.Time) *Service {
	return &Service{storage: storage, accounts: accounts, throttle: throttle, now: now}
}

// StartSignIn checks the password and chooses a session or second-factor challenge.
// The device's address also identifies the sign-in for throttling.
// The returned secret is for the browser; storage receives only its hash.
// It returns [emailaddress.ErrInvalid] for an invalid email without
// reserving a throttle attempt, and [account.ErrDeactivated] for the right
// password of a deactivated account.
func (s *Service) StartSignIn(ctx context.Context, email, password string, device Device) (SignedIn, error) {
	email, err := emailaddress.Parse(email)
	if err != nil {
		return SignedIn{}, err
	}
	id, err := s.throttle.Begin(ctx, email, device.Addr)
	if err != nil {
		return SignedIn{}, err
	}

	credentials, err := s.accounts.Authenticate(ctx, email, password)
	if err != nil {
		return SignedIn{}, err
	}
	if err := s.throttle.Succeed(ctx, id); err != nil {
		return SignedIn{}, err
	}

	var result SignedIn
	err = s.storage.StartSignIn(ctx, credentials.User.ID, func(state SignInState) (SignInChange, error) {
		// A recovery invalidates password proof even when the plaintext is unchanged.
		if state.PasswordHash != credentials.PasswordHash {
			return SignInChange{}, account.ErrInvalidCredentials
		}
		// Checked after the password, so only someone who knows it learns
		// that the account is deactivated.
		if state.Deactivated {
			return SignInChange{}, account.ErrDeactivated
		}

		// The current factor determines whether a second sign-in step is required.
		if state.Factor != nil && state.Factor.ConfirmedAt != nil {
			secret, challenge := newChallenge(credentials.User.ID, s.now)
			result = SignedIn{Step: SignInStepTOTP, Secret: secret, ExpiresAt: challenge.ExpiresAt}
			return SignInChange{Step: result.Step, Challenge: challenge, Cutoff: s.now().UTC()}, nil
		}

		issued := newSession(credentials.User.ID, device, s.now)
		result = SignedIn{Step: SignInStepComplete, Secret: issued.Secret, ExpiresAt: issued.State.ExpiresAt}
		now := s.now()
		return SignInChange{Step: result.Step, Session: issued.State, Expiry: expiryAt(now), Cutoff: now.UTC()}, nil
	})
	if errors.Is(err, ErrNotFound) {
		return SignedIn{}, account.ErrInvalidCredentials
	}
	if err != nil {
		return SignedIn{}, err
	}
	return result, nil
}
