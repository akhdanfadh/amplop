package account

import (
	"context"
	"crypto/sha256"
)

// User identifies a local account without exposing its credentials.
type User struct {
	ID    int64
	Email string
}

// Storage keeps local users and the first-admin claim.
type Storage interface {
	ReadSetup(ctx context.Context) (SetupState, error)

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
