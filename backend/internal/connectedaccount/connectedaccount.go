// Package connectedaccount owns the Gmail accounts members connect. Service
// holds the account rules: saving one once its identity is checked,
// reconnecting it, listing a member's accounts, and disconnecting one.
// Connections runs the connect flow that feeds them: a state per start, the
// callback's checks, and the save. Google's side sits behind an interface
// the google package implements.
package connectedaccount

import (
	"context"
	"errors"
	"strconv"
	"time"

	"inbok/internal/emailaddress"
)

// Errors a caller shows the member. None includes an email, subject, or
// token.
var (
	// ErrAlreadyConnected means the member has already connected this
	// Google account.
	ErrAlreadyConnected = errors.New("this Google account is already connected")
	// ErrNotFound means the member has no account with that ID, including
	// one another member connected.
	ErrNotFound = errors.New("connected account not found")
	// ErrDifferentAccount means a reconnect signed in to another Google
	// account than the one being reconnected.
	ErrDifferentAccount = errors.New("reconnected with a different Google account")
)

// Input Connections never sends; they guard other callers.
var (
	errNoSubject      = errors.New("identity has no subject")
	errNoRefreshToken = errors.New("no refresh token")
)

// Provider is the service a connected account belongs to.
type Provider string

// ProviderGmail is a Google account's Gmail, reached through the Gmail API.
const ProviderGmail Provider = "gmail"

// Identity is a Google account as Google's side of the flow read it from an
// ID token: Google's lasting ID for it and its email. Connections passes it on
// only with the refresh token from the same exchange; Service cannot check
// that pairing.
type Identity struct {
	Subject string
	Email   string
}

// Account is a connected account as members see it. It never carries the
// refresh token.
type Account struct {
	ID       int64
	UserID   int64
	Provider Provider
	Email    string
	// Subject is the provider's sub, which stays the same for the account's
	// life.
	Subject string
	// NeedsReconnectSince is when the provider refused the refresh token;
	// nil when it last accepted it.
	NeedsReconnectSince *time.Time
	CreatedAt           time.Time
}

// Credential is a sealed refresh token and the ID of the key that sealed
// it.
type Credential struct {
	KeyID      string
	Ciphertext []byte
}

// NewAccount is an account to save, before it has an ID.
type NewAccount struct {
	UserID   int64
	Provider Provider
	Email    string
	Subject  string
}

// Reconnection is what a reconnect replaces: the email Google now reports
// and the new refresh token.
type Reconnection struct {
	Email      string
	Credential Credential
}

// Storage keeps connected accounts.
type Storage interface {
	// InsertConnectedAccount gives account an ID, calls seal with it, and
	// saves account with the credential seal returns. It returns the saved
	// account, or ErrAlreadyConnected when account.UserID already has
	// account.Subject from account.Provider. An error from seal is returned
	// unchanged and saves nothing.
	InsertConnectedAccount(ctx context.Context, account NewAccount, seal func(accountID int64) (Credential, error)) (Account, error)

	// UpdateConnectedAccount locks userID's account accountID, calls update
	// with it, saves the email and credential update returns, clears
	// NeedsReconnectSince, and returns the saved account, in one
	// transaction. It returns ErrNotFound when userID has no such account.
	// An error from update rolls back and is returned unchanged.
	UpdateConnectedAccount(ctx context.Context, userID, accountID int64, update func(Account) (Reconnection, error)) (Account, error)

	// ListConnectedAccounts returns userID's accounts, oldest first.
	ListConnectedAccounts(ctx context.Context, userID int64) ([]Account, error)

	// DeleteConnectedAccount deletes userID's account accountID and returns
	// ErrNotFound when userID has no such account.
	DeleteConnectedAccount(ctx context.Context, userID, accountID int64) error
}

type sealer interface {
	Seal(plaintext []byte, owner string) (string, []byte, error)
}

// Service applies the rules for connected accounts.
type Service struct {
	storage    Storage
	encryption sealer
}

// NewService supplies storage and the keys that seal refresh tokens.
func NewService(storage Storage, encryption sealer) *Service {
	return &Service{storage: storage, encryption: encryption}
}

// Connect saves identity as a new account of userID, with refreshToken
// sealed, as a Gmail account, and returns it. It returns ErrAlreadyConnected
// when userID has already connected identity.Subject's Gmail, and
// [emailaddress.ErrInvalid] for an invalid email.
func (s *Service) Connect(ctx context.Context, userID int64, identity Identity, refreshToken string) (Account, error) {
	// 1. Refuse an identity or token that cannot be saved before any storage.
	email, err := parseIdentity(identity, refreshToken)
	if err != nil {
		return Account{}, err
	}

	// 2. Seal the token for the ID storage gives the account, and save it.
	account := NewAccount{UserID: userID, Provider: ProviderGmail, Email: email, Subject: identity.Subject}
	return s.storage.InsertConnectedAccount(ctx, account, func(accountID int64) (Credential, error) {
		return s.seal(userID, accountID, refreshToken)
	})
}

// Reconnect replaces the refresh token of userID's account accountID with
// refreshToken, takes identity's email as the account's, and returns the
// account, no longer needing a reconnect. It returns ErrNotFound when userID
// has no such account, ErrDifferentAccount when identity is another Google
// account, and [emailaddress.ErrInvalid] for an invalid email.
func (s *Service) Reconnect(ctx context.Context, userID, accountID int64, identity Identity, refreshToken string) (Account, error) {
	// 1. Refuse an identity or token that cannot be saved before any storage.
	email, err := parseIdentity(identity, refreshToken)
	if err != nil {
		return Account{}, err
	}

	// 2. Seal the token before storage locks the row; the account's ID is
	// already known.
	credential, err := s.seal(userID, accountID, refreshToken)
	if err != nil {
		return Account{}, err
	}

	// 3. Under the lock, refuse another Google account, and replace.
	return s.storage.UpdateConnectedAccount(ctx, userID, accountID, func(current Account) (Reconnection, error) {
		// Another Google account's token would read and send another
		// mailbox under this account's name and history.
		if current.Subject != identity.Subject {
			return Reconnection{}, ErrDifferentAccount
		}
		return Reconnection{Email: email, Credential: credential}, nil
	})
}

// List returns userID's accounts, oldest first.
func (s *Service) List(ctx context.Context, userID int64) ([]Account, error) {
	return s.storage.ListConnectedAccounts(ctx, userID)
}

// Disconnect deletes userID's account accountID with its credential. It
// returns ErrNotFound when userID has no such account.
func (s *Service) Disconnect(ctx context.Context, userID, accountID int64) error {
	return s.storage.DeleteConnectedAccount(ctx, userID, accountID)
}

// seal returns refreshToken sealed for userID's account accountID.
func (s *Service) seal(userID, accountID int64, refreshToken string) (Credential, error) {
	keyID, ciphertext, err := s.encryption.Seal([]byte(refreshToken), tokenOwner(userID, accountID))
	if err != nil {
		return Credential{}, err
	}
	return Credential{KeyID: keyID, Ciphertext: ciphertext}, nil
}

// parseIdentity returns identity's email as emailaddress.Parse spells it,
// or an error when identity or refreshToken cannot be saved.
func parseIdentity(identity Identity, refreshToken string) (string, error) {
	if identity.Subject == "" {
		return "", errNoSubject
	}
	if refreshToken == "" {
		return "", errNoRefreshToken
	}
	return emailaddress.Parse(identity.Email)
}

// tokenOwner returns the owner that binds a sealed refresh token to its user,
// its account, and its kind of credential (ADR006).
//
// NOTE: A ciphertext copied to another account or user fails to decrypt.
// Changing the format makes every stored refresh token unreadable.
func tokenOwner(userID, accountID int64) string {
	return "connected-account:" + strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(accountID, 10) + ":google-refresh-token"
}
