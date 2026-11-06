// Package account owns local users and their credentials.
package account

import (
	"context"
	"crypto/sha256"
)

// Role is an account's assigned role, separate from its current access.
type Role string

const (
	RoleMember Role = "member"
	RoleAdmin  Role = "admin"
)

// User identifies a local account without exposing its credentials.
type User struct {
	ID    int64
	Email string
	Role  Role
}

// Storage keeps local users and the first-admin claim.
type Storage interface {
	ReadSetup(ctx context.Context) (SetupState, error)

	// ReadCredentials finds an account by its exact email, as
	// [emailaddress.Parse] returns it.
	// A missing account returns [ErrUserNotFound].
	ReadCredentials(ctx context.Context, email string) (Credentials, error)

	// ReadUser loads current account data by ID without reading credentials.
	// A missing account returns [ErrUserNotFound].
	ReadUser(ctx context.Context, id int64) (User, error)

	// UpdateSetup locks user writes, reads setup state, then calls update.
	// It saves the returned administrator only if update and the write succeed.
	UpdateSetup(ctx context.Context, update func(SetupState) (NewAdmin, error)) (User, error)
}

// Service holds local account rules. Only the setup token's digest is kept.
type Service struct {
	storage   Storage
	tokenHash *[sha256.Size]byte // nil: no setup token configured
}

// NewService uses an empty token or one whose length was checked at startup.
func NewService(storage Storage, token string) *Service {
	service := &Service{storage: storage}
	if token != "" {
		digest := sha256.Sum256([]byte(token))
		service.tokenHash = &digest
	}
	return service
}

// UserByID returns the account's current identity and assigned role.
// A missing account returns [ErrUserNotFound].
func (s *Service) UserByID(ctx context.Context, id int64) (User, error) {
	return s.storage.ReadUser(ctx, id)
}
