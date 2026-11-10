package connectedaccount

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// tokenRenewBefore is how long before its expiry a kept access token is
// traded for a new one, inbok's choice: a margin for the step that takes
// it, which uses it for at most a page of calls, usually a minute or two
// with their retries. A call Gmail refuses as expired makes the step take
// a new one, through [Access.Forget].
const tokenRenewBefore = 5 * time.Minute

// Errors [Access.Token] returns. None includes a token or email.
var (
	// ErrNeedsReconnect means Google refused the account's refresh token,
	// now or before, and provider work for it waits until the member
	// reconnects.
	ErrNeedsReconnect = errors.New("connected account needs reconnecting")
	// ErrDeactivated means the account's member is deactivated, so no
	// provider work runs for it.
	ErrDeactivated = errors.New("connected account's member is deactivated")
	// ErrTokenUnreadable means the saved refresh token cannot be decrypted,
	// e.g. its key is no longer in INBOK_ENCRYPTION_KEYS.
	ErrTokenUnreadable = errors.New("connected account's refresh token is unreadable")
	// ErrCredentialChanged means the account's credential changed, or the
	// account went, after it was read: storage returns it for a write
	// meant for the credential read, and [Access.Token] after a second change.
	ErrCredentialChanged = errors.New("connected account's credential changed")
)

// ErrRefreshRefused means Google refused a refresh token: it was revoked,
// expired, or dropped. Google's side returns it; [Access.Token] records the
// account as needing a reconnect.
var ErrRefreshRefused = errors.New("google refused the refresh token")

// ErrRefreshFailed means Google's token endpoint failed to answer a
// refresh, e.g. during an outage, rather than refusing the token. Trying
// later may succeed.
var ErrRefreshFailed = errors.New("google failed to refresh the token")

// AccessToken is a short-lived token for one account's Gmail, about an
// hour at Google. It is never stored, and printing it with any verb or
// saving it as JSON shows nothing.
type AccessToken struct {
	value string
}

// NewAccessToken returns value as an access token.
func NewAccessToken(value string) AccessToken {
	return AccessToken{value: value}
}

// Value returns the token itself, for the request that carries it.
func (t AccessToken) Value() string {
	return t.value
}

// String returns a placeholder, so printing the token leaks nothing.
func (t AccessToken) String() string {
	return "AccessToken{...}"
}

// GoString returns the same placeholder for %#v.
func (t AccessToken) GoString() string {
	return t.String()
}

// Renewal is what Google returns for a refresh token.
type Renewal struct {
	AccessToken AccessToken
	// Expiry is when the access token stops working; zero when Google did
	// not say.
	Expiry time.Time
	// RefreshToken replaces the saved one; empty when Google keeps it.
	RefreshToken string `json:"-"`
}

// String returns r without its tokens.
func (r Renewal) String() string {
	return fmt.Sprintf("Renewal{Replaced: %t}", r.RefreshToken != "")
}

// GoString returns the same for %#v.
func (r Renewal) GoString() string {
	return r.String()
}

// AccessState is what [Access.Token] reads about an account before calling
// Google.
type AccessState struct {
	UserID     int64
	Credential Credential
	// NeedsReconnect is whether Google has refused the saved refresh token.
	NeedsReconnect bool
	// Deactivated is whether the account's member is deactivated.
	Deactivated bool
}

// refresher is Google's side of [Access.Token].
type refresher interface {
	// Refresh trades refreshToken for an access token. It returns
	// [ErrRefreshRefused] when Google refuses refreshToken.
	Refresh(ctx context.Context, refreshToken string) (Renewal, error)
}

// opener opens saved refresh tokens.
type opener interface {
	Open(id string, ciphertext []byte, owner string) ([]byte, error)
}

// keyring opens saved refresh tokens and seals their replacements.
type keyring interface {
	sealer
	opener
}

// accessStorage keeps what [Access.Token] reads and changes.
type accessStorage interface {
	// ReadConnectedAccountAccess returns account accountID's state, or
	// [ErrNotFound] when there is no such account.
	ReadConnectedAccountAccess(ctx context.Context, accountID int64) (AccessState, error)

	// ReplaceConnectedAccountToken saves next as account accountID's credential if
	// its ciphertext is still current. It returns [ErrCredentialChanged]
	// otherwise, including when the account went.
	ReplaceConnectedAccountToken(ctx context.Context, accountID int64, current []byte, next Credential) error

	// MarkConnectedAccountReconnect records at as when account accountID started needing
	// a reconnect, keeping an earlier time, if its ciphertext is still
	// current. It returns [ErrCredentialChanged] otherwise, including when the
	// account went.
	MarkConnectedAccountReconnect(ctx context.Context, accountID int64, current []byte, at time.Time) error
}

// Access hands out access tokens for connected accounts, for the jobs that
// call Gmail. It exists only when a Google client is configured.
//
// DESIGN: Each account's access token is kept in memory and handed out
// again until shortly before it expires (ADR006, "Access tokens are reused
// within a worker"), while every call still reads the account: a schedule
// that checks every account each minute would otherwise trade every
// refresh token at Google each minute. It is never stored.
type Access struct {
	storage    accessStorage
	encryption keyring
	google     refresher
	now        func() time.Time

	mu   sync.Mutex
	kept map[int64]keptToken
}

// keptToken is an access token [Access] hands out again, with when it
// expires and the sealed refresh token it was traded for.
type keptToken struct {
	token      AccessToken
	expiry     time.Time
	ciphertext []byte
}

// NewAccess returns token access given storage, the keys that sealed the
// refresh tokens, a configured Google client, and a clock.
func NewAccess(storage accessStorage, encryption keyring, google refresher, now func() time.Time) *Access {
	return &Access{storage: storage, encryption: encryption, google: google, now: now, kept: make(map[int64]keptToken)}
}

// Token returns an access token for account accountID's Gmail: one it traded at
// Google for the saved refresh token before, while that token is still saved
// and more than tokenRenewBefore remains of it, or else a new one. A job takes
// one in each step that calls Gmail. Its checks hold when it last reads the
// account, just before returning; a job that must not act for an account gone
// or a member deactivated since checks again where it acts.
//
// It returns [ErrNotFound] when the account is gone, [ErrDeactivated],
// [ErrNeedsReconnect] when Google refuses the refresh token, now or before,
// [ErrTokenUnreadable], [ErrCredentialChanged] when the credential changed
// twice while this call held it, an error wrapping [ErrRefreshFailed] when
// Google's token endpoint failed, and any other error, e.g. storage's, as a
// failure that may pass.
//
// DESIGN: a refresh token Google replaces is left alone at Google, not revoked:
// revoking it would end the grant the replacement belongs to (ADR006).
func (a *Access) Token(ctx context.Context, accountID int64) (AccessToken, error) {
	token, err := a.token(ctx, accountID)
	// A credential that changed while this call held it, by a reconnect or
	// another job's replacement, is read again once: the new one may work
	// where the old did not.
	if errors.Is(err, ErrCredentialChanged) {
		return a.token(ctx, accountID)
	}
	return token, err
}

// Forget drops the access token kept for account accountID, so the next
// [Access.Token] trades the refresh token again: Gmail refused the kept
// one, e.g. after the member revoked inbok's access at Google.
func (a *Access) Forget(accountID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.kept, accountID)
}

// Check returns nil when provider work may still run for account
// accountID: the account exists, needs no reconnect, its member is active,
// and its refresh token opens. It asks Google nothing, so it suits a step
// that only writes inbok's own data. It returns the same stop errors as
// [Access.Token]: [ErrNotFound], [ErrDeactivated], [ErrNeedsReconnect], or
// [ErrTokenUnreadable]; any other error is storage's.
func (a *Access) Check(ctx context.Context, accountID int64) error {
	state, err := a.read(ctx, accountID)
	if err != nil {
		return err
	}
	_, err = a.encryption.Open(state.Credential.KeyID, state.Credential.Ciphertext, tokenOwner(state.UserID, accountID))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTokenUnreadable, err)
	}
	return nil
}

// token is one attempt of [Access.Token], returning [ErrCredentialChanged]
// when the credential it read changed before it could write.
func (a *Access) token(ctx context.Context, accountID int64) (AccessToken, error) {
	// 1. Read the account, and stop before Google for one whose provider
	// work has stopped, dropping any token kept for it.
	state, err := a.read(ctx, accountID)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrDeactivated) || errors.Is(err, ErrNeedsReconnect) {
		a.Forget(accountID)
	}
	if err != nil {
		return AccessToken{}, err
	}

	// 2. Open the refresh token. An unreadable one is not recorded as
	// needing a reconnect: a worker started with a wrong key list would
	// mark every account (ADR006).
	refreshToken, err := openToken(a.encryption, state.UserID, accountID, state.Credential)
	if err != nil {
		return AccessToken{}, err
	}

	// 3. Hand out the token kept for this credential while it lasts. A
	// reconnect or a replacement saved other bytes, so it is traded anew.
	if token, ok := a.keptFor(accountID, state.Credential.Ciphertext); ok {
		return token, nil
	}

	// 4. Trade it at Google. A refusal is recorded only against the token
	// refused: one a reconnect saved meanwhile has not been tried.
	renewal, err := a.google.Refresh(ctx, string(refreshToken))
	if errors.Is(err, ErrRefreshRefused) {
		if err := a.storage.MarkConnectedAccountReconnect(ctx, accountID, state.Credential.Ciphertext, a.now().UTC()); err != nil {
			return AccessToken{}, err
		}
		return AccessToken{}, ErrNeedsReconnect
	}
	if err != nil {
		return AccessToken{}, fmt.Errorf("%w: %w", ErrRefreshFailed, err)
	}

	// 5. Save a replacement before the access token is used, so the old
	// refresh token is dropped only once the new one is stored.
	saved := state.Credential.Ciphertext
	if renewal.RefreshToken != "" {
		next, err := seal(a.encryption, state.UserID, accountID, renewal.RefreshToken)
		if err != nil {
			return AccessToken{}, err
		}
		if err := a.storage.ReplaceConnectedAccountToken(ctx, accountID, saved, next); err != nil {
			return AccessToken{}, err
		}
		saved = next.Ciphertext
	}

	// 6. Read the account again, so a disconnect, deactivation, or refusal
	// while Google answered stops the job too, and a credential changed
	// meanwhile is read again.
	latest, err := a.read(ctx, accountID)
	if err != nil {
		return AccessToken{}, err
	}
	if !bytes.Equal(latest.Credential.Ciphertext, saved) {
		return AccessToken{}, ErrCredentialChanged
	}

	// 7. Keep it for the next calls, under the credential now saved.
	a.keep(accountID, renewal, saved)
	return renewal.AccessToken, nil
}

// keptFor returns the access token kept for account accountID if it was
// traded for the sealed refresh token ciphertext and more than
// tokenRenewBefore remains of it.
func (a *Access) keptFor(accountID int64, ciphertext []byte) (AccessToken, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	kept, ok := a.kept[accountID]
	if !ok || !bytes.Equal(kept.ciphertext, ciphertext) || !a.now().Before(kept.expiry.Add(-tokenRenewBefore)) {
		return AccessToken{}, false
	}
	return kept.token, true
}

// keep keeps renewal's access token for account accountID, traded for the
// sealed refresh token ciphertext, unless Google gave no expiry, and drops
// the expired tokens of other accounts, e.g. one disconnected since.
func (a *Access) keep(accountID int64, renewal Renewal, ciphertext []byte) {
	if renewal.Expiry.IsZero() {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for id, kept := range a.kept {
		if !now.Before(kept.expiry) {
			delete(a.kept, id)
		}
	}
	a.kept[accountID] = keptToken{token: renewal.AccessToken, expiry: renewal.Expiry, ciphertext: ciphertext}
}

// read returns account accountID's state. It returns [ErrNotFound],
// [ErrDeactivated], or [ErrNeedsReconnect] when provider work for it has
// stopped.
func (a *Access) read(ctx context.Context, accountID int64) (AccessState, error) {
	state, err := a.storage.ReadConnectedAccountAccess(ctx, accountID)
	if err != nil {
		return AccessState{}, err
	}
	switch {
	case state.Deactivated:
		return AccessState{}, ErrDeactivated
	case state.NeedsReconnect:
		return AccessState{}, ErrNeedsReconnect
	}
	return state, nil
}
