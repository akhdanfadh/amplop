package session

import (
	"context"
	"crypto/sha256"
	"uuid"
)

// Reference identifies a session checked by [Service.Current], keeping its hash
// private. Its zero value does not identify a checked session.
type Reference struct {
	hash [sha256.Size]byte
	id   uuid.UUID
}

// ID returns the session's public ID, which names it to its owner and lets
// other features tie their rows to it. It is the zero UUID for a reference
// Current did not return.
func (r Reference) ID() uuid.UUID {
	return r.id
}

// SignOut deletes the checked session. Deleting an absent row succeeds.
func (s *Service) SignOut(ctx context.Context, ref Reference) error {
	if ref == (Reference{}) {
		return ErrUnauthenticated
	}
	return s.storage.DeleteSession(ctx, ref.hash)
}
