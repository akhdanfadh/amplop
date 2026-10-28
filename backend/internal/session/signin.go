package session

import (
	"context"
	"net/netip"
	"time"

	"inbok/internal/account"
)

// accountService verifies account credentials without exposing stored hashes.
type accountService interface {
	Authenticate(ctx context.Context, email, password string) (account.User, error)
}

// Service creates browser sessions after throttled credential verification.
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

// SignIn checks credentials within the failure limits and saves a new session.
// The returned secret is for the browser; storage receives only its hash.
func (s *Service) SignIn(ctx context.Context, email, password string, addr netip.Addr) (Issued, error) {
	id, err := s.throttle.Begin(ctx, email, addr)
	if err != nil {
		return Issued{}, err
	}

	user, err := s.accounts.Authenticate(ctx, email, password)
	if err != nil {
		return Issued{}, err
	}
	if err := s.throttle.Succeed(ctx, id); err != nil {
		return Issued{}, err
	}

	issued := newSession(user.ID, s.now)
	if err := s.storage.InsertSession(ctx, issued.State); err != nil {
		return Issued{}, err
	}
	return issued, nil
}
