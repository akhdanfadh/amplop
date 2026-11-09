package connectedaccount

import (
	"context"
	"errors"
	"fmt"
)

// RevokeOutcome is what became of a disconnected account's refresh token.
type RevokeOutcome string

const (
	// RevokeRevoked means Google ended the token's grant, or no longer knew
	// the token.
	RevokeRevoked RevokeOutcome = "revoked"
	// RevokeShared means another connected account is the same Google
	// account, so the grant was left alone.
	RevokeShared RevokeOutcome = "shared"
	// RevokeUnreadable means no key opened the token, so nothing could be
	// sent to Google.
	RevokeUnreadable RevokeOutcome = "unreadable"
	// RevokeGone means the removal was already done or discarded.
	RevokeGone RevokeOutcome = "gone"
)

// Removal is what is left to do for a disconnected account after its row
// is gone: so far, revoking its refresh token, kept until a job does.
type Removal struct {
	// UserID and [Removal.AccountID] are the disconnected account's, which
	// the sealed token is bound to.
	UserID     int64
	AccountID  int64
	Credential Credential
	// Shared is whether a connected account in the instance is the same
	// Google account.
	Shared bool
}

// revoker is Google's side of [Removals.Revoke].
type revoker interface {
	// Revoke ends refreshToken's grant at Google. A token Google no longer
	// knows counts as revoked.
	Revoke(ctx context.Context, refreshToken string) error
}

// removalStorage keeps the tokens of disconnected accounts.
type removalStorage interface {
	// ReadAccountRemoval returns removal removalID, or [ErrNotFound]
	// when there is none.
	ReadAccountRemoval(ctx context.Context, removalID int64) (Removal, error)
	// DeleteAccountRemoval deletes removal removalID; deleting one
	// already gone succeeds.
	DeleteAccountRemoval(ctx context.Context, removalID int64) error
}

// Removals finishes removing disconnected accounts, for the job a disconnect
// saves: so far, it revokes their refresh tokens at Google. It exists only when
// a Google client is configured.
type Removals struct {
	storage    removalStorage
	encryption opener
	google     revoker
}

// NewRemovals returns removals given storage, the keys that sealed the
// refresh tokens, and a configured Google client.
func NewRemovals(storage removalStorage, encryption opener, google revoker) *Removals {
	return &Removals{storage: storage, encryption: encryption, google: google}
}

// Revoke revokes the token of removal removalID at Google, unless
// another connected account is the same Google account, then deletes the
// removal. It returns what became of the token, or Google's error, after
// which the removal stays for another try. Running it again after a
// success returns [RevokeGone].
//
// DESIGN: the same Google account connected by another member shares the
// grant, which revoking would end too (ADR006). The check runs here rather
// than at the disconnect, so it sees connections made since. A member who
// connects the same Google account between the check and Google's answer
// still loses that connection; the window is one call to Google.
func (r *Removals) Revoke(ctx context.Context, removalID int64) (RevokeOutcome, error) {
	// 1. Read the removal; one already done leaves nothing to do.
	removal, err := r.storage.ReadAccountRemoval(ctx, removalID)
	if errors.Is(err, ErrNotFound) {
		return RevokeGone, nil
	}
	if err != nil {
		return "", err
	}

	// 2. Leave a grant another connection uses alone.
	if removal.Shared {
		return RevokeShared, r.storage.DeleteAccountRemoval(ctx, removalID)
	}

	// 3. Open the token. No key opens it on any later try either, so the
	// removal goes.
	credential := removal.Credential
	token, err := r.encryption.Open(credential.KeyID, credential.Ciphertext, tokenOwner(removal.UserID, removal.AccountID))
	if err != nil {
		return RevokeUnreadable, r.storage.DeleteAccountRemoval(ctx, removalID)
	}

	// 4. Revoke it at Google before deleting, so a failure leaves the
	// removal for another try.
	if err := r.google.Revoke(ctx, string(token)); err != nil {
		return "", fmt.Errorf("revoke token: %w", err)
	}
	return RevokeRevoked, r.storage.DeleteAccountRemoval(ctx, removalID)
}

// Discard deletes removal removalID without revoking its token, for a
// job that gave up on Google: the token is left to lapse rather than kept.
func (r *Removals) Discard(ctx context.Context, removalID int64) error {
	return r.storage.DeleteAccountRemoval(ctx, removalID)
}
