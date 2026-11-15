package totp

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"strings"
	"time"

	"inbok/internal/randomsecret"
)

var errDisplayCodes = errors.New("invalid stored backup-code display copy")

// waitingSet is a new backup-code set ready to wait: the codes to show, their
// hashes, a one-time token with its hash, and, once sealed, a display copy.
type waitingSet struct {
	set        BackupCodes
	keyID      string
	ciphertext []byte
	token      string
	tokenHash  [sha256.Size]byte
}

// newWaitingSet makes ten backup codes and the token that activates them. It
// returns the context's error if ctx ends while waiting to hash.
func newWaitingSet(ctx context.Context) (waitingSet, error) {
	set, err := NewBackupCodes(ctx)
	if err != nil {
		return waitingSet{}, err
	}
	token, tokenHash := randomsecret.New()
	return waitingSet{set: set, token: token, tokenHash: tokenHash}, nil
}

// sealCopy seals a display copy of the codes, bound to userID, so an
// enrollment's codes can be shown again until the person says they saved
// them.
//
// DESIGN: the copy is encrypted like the authenticator secret and deleted
// when the set becomes active, so active codes are only ever stored hashed
// (NIST SP 800-63B, look-up secrets). A replacement set keeps no copy:
// [Service.ShowWaiting] says why.
func (w *waitingSet) sealCopy(encryption sealer, userID int64) error {
	keyID, ciphertext, err := encryption.Seal([]byte(strings.Join(w.set.Codes, "\n")), codesOwner(userID))
	if err != nil {
		return err
	}
	w.keyID, w.ciphertext = keyID, ciphertext
	return nil
}

// waiting returns the stored form of the set, expiring at expiresAt.
func (w waitingSet) waiting(expiresAt time.Time) *Waiting {
	return &Waiting{
		Hashes:     w.set.Hashes,
		KeyID:      w.keyID,
		Ciphertext: w.ciphertext,
		TokenHash:  w.tokenHash,
		ExpiresAt:  expiresAt.UTC(),
	}
}

// codes returns what the person sees: the codes and the token.
func (w waitingSet) codes() Codes {
	return Codes{Codes: w.set.Codes, Token: w.token}
}

// parseDisplayCodes reads a display copy [waitingSet.sealCopy] sealed.
func parseDisplayCodes(plaintext []byte) ([]string, error) {
	codes := strings.Split(string(plaintext), "\n")
	if len(codes) != backupSetSize {
		return nil, errDisplayCodes
	}
	return codes, nil
}

// codesOwner binds a sealed display copy to its user, apart from the
// authenticator secret, so neither ciphertext opens as the other.
func codesOwner(userID int64) string {
	return "totp-backup-codes:" + strconv.FormatInt(userID, 10)
}
