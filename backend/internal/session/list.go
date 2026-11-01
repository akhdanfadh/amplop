package session

import (
	"context"
	"net/netip"
	"time"
	"uuid"

	"github.com/medama-io/go-useragent"
)

// Listed is a live session as its owner sees it, without its secret or hash.
// Browser and OS are empty when the stored User-Agent does not tell them,
// and SignedInFrom is invalid when no address was stored.
type Listed struct {
	ID           uuid.UUID
	Browser      string
	OS           string
	SignedInFrom netip.Addr
	CreatedAt    time.Time
	LastUsedAt   time.Time
	Current      bool
}

// List returns the user's live sessions in storage's order, newest sign-in
// first. checkSession drops expired rows, as Current would, but List
// deletes nothing.
func (s *Service) List(ctx context.Context, current Authenticated) ([]Listed, error) {
	userID := current.Identity.User.ID
	if current.Session == (Reference{}) || userID <= 0 {
		return nil, ErrUnauthenticated
	}

	states, err := s.storage.ReadUserSessions(ctx, userID)
	if err != nil {
		return nil, err
	}

	listed := make([]Listed, 0, len(states))
	for _, state := range states {
		if _, err := checkSession(state, s.now); err != nil {
			continue
		}
		browser, os := describeUserAgent(state.Device.UserAgent)
		listed = append(listed, Listed{
			ID: state.ID, Browser: browser, OS: os, SignedInFrom: state.Device.Addr,
			CreatedAt: state.CreatedAt, LastUsedAt: state.LastUsedAt,
			Current: state.TokenHash == current.Session.hash,
		})
	}
	return listed, nil
}

// describeUserAgent names the browser and operating system for display.
// The parser builds its lookup tables once, on first use.
func describeUserAgent(userAgent string) (browser, os string) {
	if userAgent == "" {
		return "", ""
	}
	agent := useragent.NewParser().Parse(userAgent)
	return agent.Browser().String(), agent.OS().String()
}
