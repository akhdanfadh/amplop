package session

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"inbok/internal/account"
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

// SignInChange saves exactly one of Session or Challenge, selected by Step.
type SignInChange struct {
	Step      SignInStep
	Session   State
	Challenge Challenge
	Cutoff    time.Time
}

// accountService verifies password credentials.
type accountService interface {
	Authenticate(ctx context.Context, email, password string) (account.User, error)
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
// The returned secret is for the browser; storage receives only its hash.
func (s *Service) StartSignIn(ctx context.Context, email, password string, addr netip.Addr) (SignedIn, error) {
	id, err := s.throttle.Begin(ctx, email, addr)
	if err != nil {
		return SignedIn{}, err
	}

	user, err := s.accounts.Authenticate(ctx, email, password)
	if err != nil {
		return SignedIn{}, err
	}
	if err := s.throttle.Succeed(ctx, id); err != nil {
		return SignedIn{}, err
	}

	var result SignedIn
	err = s.storage.StartSignIn(ctx, user.ID, func(factor *totp.Factor) (SignInChange, error) {
		// Read under the owner lock so enrollment cannot race a password-only session.
		if factor != nil && factor.ConfirmedAt != nil {
			secret, challenge := newChallenge(user.ID, s.now)
			result = SignedIn{Step: SignInStepTOTP, Secret: secret, ExpiresAt: challenge.ExpiresAt}
			return SignInChange{Step: result.Step, Challenge: challenge, Cutoff: s.now().UTC()}, nil
		}

		issued := newSession(user.ID, s.now)
		result = SignedIn{Step: SignInStepComplete, Secret: issued.Secret, ExpiresAt: issued.State.ExpiresAt}
		return SignInChange{Step: result.Step, Session: issued.State, Cutoff: s.now().UTC()}, nil
	})
	if errors.Is(err, ErrNotFound) {
		return SignedIn{}, account.ErrInvalidCredentials
	}
	if err != nil {
		return SignedIn{}, err
	}
	return result, nil
}
