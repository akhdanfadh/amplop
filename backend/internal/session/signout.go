package session

import (
	"context"
	"crypto/sha256"
)

// Reference identifies a session checked by [Service.Current], keeping its hash
// private. Its zero value does not identify a checked session.
type Reference struct {
	hash [sha256.Size]byte
}

// SignOut deletes the checked session. Deleting an absent row succeeds.
func (s *Service) SignOut(ctx context.Context, ref Reference) error {
	if ref == (Reference{}) {
		return ErrUnauthenticated
	}
	return s.storage.DeleteSession(ctx, ref.hash)
}
