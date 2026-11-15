package session

import (
	"context"
	"errors"
	"net/netip"
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

// SignInState is the current password hash, role, factor, and status, read
// with the user locked.
type SignInState struct {
	PasswordHash string
	Role         account.Role
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
	// 1. Refuse an invalid email before reserving an attempt.
	email, err := emailaddress.Parse(email)
	if err != nil {
		return SignedIn{}, err
	}

	// 2. Reserve an attempt before the password hash, so a refused guess
	// costs no hash.
	id, err := s.throttle.Begin(ctx, email, device.Addr)
	if err != nil {
		return SignedIn{}, err
	}

	// 3. Verify the password. A failure keeps the attempt, so it counts.
	credentials, err := s.accounts.Authenticate(ctx, email, password)
	if err != nil {
		return SignedIn{}, err
	}

	// 4. Remove the attempt: only failures count.
	if err := s.throttle.Succeed(ctx, id); err != nil {
		return SignedIn{}, err
	}

	// 5. Under the user's lock, check the proof again and issue a session
	// or a code challenge.
	var result issuedSignIn
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
		if factorConfirmed(state.Factor) {
			secret, challenge := newChallenge(credentials.User.ID, s.now)
			result = issuedSignIn{SignedIn: SignedIn{Step: SignInStepTOTP, Secret: secret, ExpiresAt: challenge.ExpiresAt}}
			return SignInChange{Step: result.Step, Challenge: challenge, Cutoff: s.now().UTC()}, nil
		}

		result = s.signedIn(credentials.User.ID, state, device)
		return s.sessionChange(result), nil
	})
	if errors.Is(err, ErrNotFound) {
		return SignedIn{}, account.ErrInvalidCredentials
	}
	if err != nil {
		return SignedIn{}, err
	}
	return result.SignedIn, nil
}

// VerifyPassword confirms password is user's current password, counting the
// attempt against the sign-in limits for user's email from addr, so a
// session cookie alone cannot be used to guess it. It returns
// [account.ErrInvalidCredentials] for a wrong password and an
// [*attemptlimit.Error] wrapping [ErrThrottled] when a limit is reached.
func (s *Service) VerifyPassword(ctx context.Context, user account.User, password string, addr netip.Addr) error {
	// 1. Reserve an attempt before the password hash.
	id, err := s.throttle.Begin(ctx, user.Email, addr)
	if err != nil {
		return err
	}

	// 2. Verify the password. A failure keeps the attempt, so it counts.
	credentials, err := s.accounts.Authenticate(ctx, user.Email, password)
	if err != nil {
		return err
	}
	if credentials.User.ID != user.ID {
		return account.ErrInvalidCredentials
	}

	// 3. Remove the attempt: only failures count.
	return s.throttle.Succeed(ctx, id)
}

// issuedSignIn is a complete sign-in's cookie and the session to save.
type issuedSignIn struct {
	SignedIn
	state State
}

// signedIn issues a complete sign-in's session for userID with state's
// role and factor. An administrator without a confirmed factor gets a
// session that lives only [enrollmentSessionLifetime]; finishing
// enrollment replaces it with a full session.
func (s *Service) signedIn(userID int64, state SignInState, device Device) issuedSignIn {
	lifetime := maxLifetime
	if state.Role == account.RoleAdmin && !factorConfirmed(state.Factor) {
		lifetime = enrollmentSessionLifetime
	}
	issued := newSession(userID, device, lifetime, s.now)
	return issuedSignIn{
		SignedIn: SignedIn{Step: SignInStepComplete, Secret: issued.Secret, ExpiresAt: issued.State.ExpiresAt},
		state:    issued.State,
	}
}

// sessionChange returns the change that saves issued's session and prunes
// the user's expired sessions.
func (s *Service) sessionChange(issued issuedSignIn) SignInChange {
	now := s.now()
	return SignInChange{Step: SignInStepComplete, Session: issued.state, Expiry: expiryAt(now), Cutoff: now.UTC()}
}

// factorConfirmed reports whether factor exists and is confirmed.
func factorConfirmed(factor *totp.Factor) bool {
	return factor != nil && factor.ConfirmedAt != nil
}
