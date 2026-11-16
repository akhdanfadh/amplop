// Package connectedaccount owns the Gmail accounts members connect. [Service]
// holds the account rules: saving one once its identity is checked,
// reconnecting it, listing a member's accounts, and disconnecting one.
// [Connections] runs the connect flow that feeds them: a state per start, the
// callback's checks, and the save. [Access] hands the jobs that call Gmail an
// access token per account, saving what Google says about the refresh
// token. Google's side sits behind interfaces the google package
// implements.
package connectedaccount

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	// ErrNameTooLong means an account's name is longer than
	// [MaxNameLength] characters.
	ErrNameTooLong = errors.New("account name too long")
	// ErrNameInvalid means an account's name holds a control character or
	// another line break.
	ErrNameInvalid = errors.New("account name has a control character")
)

// MaxNameLength is the most characters an account's name may have:
// inbok's choice, enough for a name beside the address in the sidebar.
const MaxNameLength = 64

// Input [Connections] never sends; they guard other callers.
var (
	errNoSubject      = errors.New("identity has no subject")
	errNoRefreshToken = errors.New("no refresh token")
)

// Provider is the service a connected account belongs to.
type Provider string

// ProviderGmail is a Google account's Gmail, reached through the Gmail API.
const ProviderGmail Provider = "gmail"

// Identity is a Google account as Google's side of the flow read it from an
// ID token: Google's lasting ID for it and its email. [Connections] passes it
// on only with the refresh token from the same exchange; [Service] cannot
// check
// that pairing.
type Identity struct {
	Subject string
	Email   string
}

// Status says whether inbok can use an account's permission at Google now.
type Status string

const (
	// StatusUsable is an account whose saved refresh token opens and that
	// Google has not refused.
	StatusUsable Status = "usable"
	// StatusNeedsReconnect is an account whose refresh token Google refused;
	// [Account.NeedsReconnectSince] says when.
	StatusNeedsReconnect Status = "needs-reconnect"
	// StatusTokenUnreadable is an account whose saved refresh token no
	// configured key opens. It is worked out at each read, not saved, so
	// restoring the key makes the account usable again (ADR006).
	StatusTokenUnreadable Status = "token-unreadable"
)

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
	// Status is whether inbok can use the account now, as [Service] read it.
	Status Status
	// Name is what the member calls the account, shown instead of its
	// address; empty until they choose one.
	Name string
	// Color is the account's mark, 1 to 8, one of the account colours of
	// web/DESIGN.md. [Storage] gives a new account the lowest the member's
	// other accounts use least.
	Color int
}

// Action is something a member can ask of one of their connected accounts.
type Action string

const (
	ActionSync       Action = "sync"       // sync it now
	ActionSend       Action = "send"       // send messages from it
	ActionReconnect  Action = "reconnect"  // give inbok its permission again
	ActionRename     Action = "rename"     // set or clear its name
	ActionDisconnect Action = "disconnect" // delete it from inbok
)

// Actions are every [Action], in the order the API lists them.
var Actions = []Action{ActionSync, ActionSend, ActionReconnect, ActionRename, ActionDisconnect}

// AllowedActions returns the actions accepted for a now; offered says
// whether its provider can be connected, i.e. an administrator set up its
// client. Syncing needs the provider and a permission the provider still
// accepts; sending needs the provider and a usable [Account.Status];
// reconnecting needs the provider; renaming and disconnecting are always
// accepted.
//
// DESIGN: The services refuse the rest: mailsync refuses a sync while
// [Account.NeedsReconnectSince] is set, outgoingmessage refuses a send
// from an account whose permission the provider refused or no key opens,
// and the HTTP API refuses a sync, send, or reconnect when no client is
// configured. An unreadable token still syncs: the sync records why it
// failed, which the account's sync shows.
func (a Account) AllowedActions(offered bool) []Action {
	actions := make([]Action, 0, len(Actions))
	if offered && a.NeedsReconnectSince == nil {
		actions = append(actions, ActionSync)
	}
	if offered && a.Status == StatusUsable {
		actions = append(actions, ActionSend)
	}
	if offered {
		actions = append(actions, ActionReconnect)
	}
	return append(actions, ActionRename, ActionDisconnect)
}

// StoredAccount is an account with its sealed refresh token, as storage
// keeps it.
type StoredAccount struct {
	Account
	Credential Credential
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
	// saves account with the credential seal returns, its sync state,
	// waiting, and the job of its first full sync, in one transaction. It
	// returns the saved account, or [ErrAlreadyConnected] when
	// account.UserID already has account.Subject from account.Provider. An
	// error from seal is returned unchanged; any error saves nothing.
	InsertConnectedAccount(ctx context.Context, account NewAccount, seal func(accountID int64) (Credential, error)) (Account, error)

	// UpdateConnectedAccount locks userID's account accountID, calls update
	// with it, saves the email and credential update returns, clears
	// NeedsReconnectSince, and returns the saved account, in one
	// transaction. It returns [ErrNotFound] when userID has no such account.
	// An error from update rolls back and is returned unchanged.
	UpdateConnectedAccount(ctx context.Context, userID, accountID int64, update func(Account) (Reconnection, error)) (Account, error)

	// ListConnectedAccounts returns userID's accounts with their
	// credentials, oldest first.
	ListConnectedAccounts(ctx context.Context, userID int64) ([]StoredAccount, error)

	// RenameConnectedAccount sets the name of userID's account accountID,
	// clearing it when empty, and returns the account with its credential,
	// or [ErrNotFound] when userID has no such account.
	RenameConnectedAccount(ctx context.Context, userID, accountID int64, name string) (StoredAccount, error)

	// DeleteConnectedAccount deletes userID's account accountID, keeps its
	// credential as a removal, and saves the jobs that revoke it, delete its
	// stored content, and remove its messages from the search index, in one
	// transaction. It returns the deleted account with its credential, or
	// [ErrNotFound] when userID has no such account.
	DeleteConnectedAccount(ctx context.Context, userID, accountID int64) (StoredAccount, error)
}

type sealer interface {
	Seal(plaintext []byte, owner string) (string, []byte, error)
}

// Service applies the rules for connected accounts.
type Service struct {
	storage    Storage
	encryption keyring
}

// NewService supplies storage and the keys that seal refresh tokens and
// open them to check they still can be.
func NewService(storage Storage, encryption keyring) *Service {
	return &Service{storage: storage, encryption: encryption}
}

// Connect saves identity as a new account of userID, with refreshToken
// sealed, as a Gmail account, and returns it. It returns [ErrAlreadyConnected]
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
	return usable(s.storage.InsertConnectedAccount(ctx, account, func(accountID int64) (Credential, error) {
		return seal(s.encryption, userID, accountID, refreshToken)
	}))
}

// Reconnect replaces the refresh token of userID's account accountID with
// refreshToken, takes identity's email as the account's, and returns the
// account, no longer needing a reconnect. It returns [ErrNotFound] when userID
// has no such account, [ErrDifferentAccount] when identity is another Google
// account, and [emailaddress.ErrInvalid] for an invalid email.
func (s *Service) Reconnect(ctx context.Context, userID, accountID int64, identity Identity, refreshToken string) (Account, error) {
	// 1. Refuse an identity or token that cannot be saved before any storage.
	email, err := parseIdentity(identity, refreshToken)
	if err != nil {
		return Account{}, err
	}

	// 2. Seal the token before storage locks the row; the account's ID is
	// already known.
	credential, err := seal(s.encryption, userID, accountID, refreshToken)
	if err != nil {
		return Account{}, err
	}

	// 3. Under the lock, refuse another Google account, and replace.
	return usable(s.storage.UpdateConnectedAccount(ctx, userID, accountID, func(current Account) (Reconnection, error) {
		// Another Google account's token would read and send another
		// mailbox under this account's name and history.
		if current.Subject != identity.Subject {
			return Reconnection{}, ErrDifferentAccount
		}
		return Reconnection{Email: email, Credential: credential}, nil
	}))
}

// List returns userID's accounts, oldest first, each with its status now.
func (s *Service) List(ctx context.Context, userID int64) ([]Account, error) {
	stored, err := s.storage.ListConnectedAccounts(ctx, userID)
	if err != nil {
		return nil, err
	}
	accounts := make([]Account, 0, len(stored))
	for _, account := range stored {
		accounts = append(accounts, s.withStatus(account))
	}
	return accounts, nil
}

// Rename sets the name of userID's account accountID to name without its
// leading and trailing white space, or clears it when that leaves nothing,
// and returns the account. It returns [ErrNameTooLong] past
// [MaxNameLength] characters, [ErrNameInvalid] for a control character or
// line break, and [ErrNotFound] when userID has no such account.
func (s *Service) Rename(ctx context.Context, userID, accountID int64, name string) (Account, error) {
	parsed, err := parseName(name)
	if err != nil {
		return Account{}, err
	}
	stored, err := s.storage.RenameConnectedAccount(ctx, userID, accountID, parsed)
	if err != nil {
		return Account{}, err
	}
	return s.withStatus(stored), nil
}

// parseName returns name without its leading and trailing white space,
// empty to clear a name. It returns [ErrNameTooLong] past [MaxNameLength]
// characters, and [ErrNameInvalid] for a control character or a Unicode
// line or paragraph separator, which would break the line it is shown on.
func parseName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > MaxNameLength {
		return "", ErrNameTooLong
	}
	if strings.ContainsFunc(name, breaksLine) {
		return "", ErrNameInvalid
	}
	return name, nil
}

// breaksLine reports whether r is a control character, e.g. a line feed,
// or a Unicode line or paragraph separator, U+2028 or U+2029.
func breaksLine(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp)
}

// Disconnect deletes userID's account accountID with its credential, which a
// job then revokes at Google ([Removals.Revoke]), and returns the deleted
// account with the status it had. It returns [ErrNotFound] when userID has
// no such account.
func (s *Service) Disconnect(ctx context.Context, userID, accountID int64) (Account, error) {
	deleted, err := s.storage.DeleteConnectedAccount(ctx, userID, accountID)
	if err != nil {
		return Account{}, err
	}
	return s.withStatus(deleted), nil
}

// CheckToken returns [ErrTokenUnreadable] when no configured key opens
// credential, the sealed refresh token of userID's account accountID, and
// nil when one does.
func (s *Service) CheckToken(userID, accountID int64, credential Credential) error {
	token, err := openToken(s.encryption, userID, accountID, credential)
	// Only whether it opens matters; the token leaves memory sooner.
	clear(token)
	return err
}

// withStatus returns stored's account with its status: needing a reconnect
// once Google refused its token, else whether a configured key opens it.
func (s *Service) withStatus(stored StoredAccount) Account {
	account := stored.Account
	switch {
	case account.NeedsReconnectSince != nil:
		account.Status = StatusNeedsReconnect
	case s.CheckToken(account.UserID, account.ID, stored.Credential) != nil:
		account.Status = StatusTokenUnreadable
	default:
		account.Status = StatusUsable
	}
	return account
}

// usable returns account, just saved with a newly sealed refresh token, as
// usable, and err as is.
func usable(account Account, err error) (Account, error) {
	if err != nil {
		return Account{}, err
	}
	account.Status = StatusUsable
	return account, nil
}

// seal returns refreshToken sealed with encryption for userID's account
// accountID.
func seal(encryption sealer, userID, accountID int64, refreshToken string) (Credential, error) {
	keyID, ciphertext, err := encryption.Seal([]byte(refreshToken), tokenOwner(userID, accountID))
	if err != nil {
		return Credential{}, err
	}
	return Credential{KeyID: keyID, Ciphertext: ciphertext}, nil
}

// openToken returns credential, the sealed refresh token of userID's account
// accountID, opened with keys, or an error wrapping [ErrTokenUnreadable].
func openToken(keys opener, userID, accountID int64, credential Credential) ([]byte, error) {
	token, err := keys.Open(credential.KeyID, credential.Ciphertext, tokenOwner(userID, accountID))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenUnreadable, err)
	}
	return token, nil
}

// parseIdentity returns identity's email as [emailaddress.Parse] spells it,
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
