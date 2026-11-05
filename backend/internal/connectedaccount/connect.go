package connectedaccount

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
	"uuid"

	"inbok/internal/emailaddress"
	"inbok/internal/randomsecret"
)

// stateLifetime is how long a member has to finish at Google: long enough to
// sign in and read the consent page, short enough that a stray link soon
// stops working.
const stateLifetime = 10 * time.Minute

// errorAccessDenied is the error Google's redirect carries when the member
// declines (RFC 6749 section 4.1.2.1).
const errorAccessDenied = "access_denied"

// Errors that end the connect flow. None includes a state, code, token, or
// email.
var (
	// ErrStateInvalid means the callback's state is unknown, used, expired,
	// or from another session.
	ErrStateInvalid = errors.New("connect state is invalid or expired")
	// ErrCancelled means the member declined on Google's consent page.
	ErrCancelled = errors.New("connecting was cancelled at Google")
	// ErrGmailNotGranted means the member unticked Gmail access.
	ErrGmailNotGranted = errors.New("gmail access was not granted")
	// ErrEmailNotVerified means Google does not vouch for the account's email.
	ErrEmailNotVerified = errors.New("google has not verified the account's email")
)

// Answers from Google the member cannot fix, all shown as a failure.
var (
	errNoSession      = errors.New("no session")
	errAuthorization  = errors.New("google refused the authorization request")
	errNoCode         = errors.New("callback has no code")
	errNoGrantToken   = errors.New("google sent no refresh token")
	errProfileMailbox = errors.New("gmail profile is for another mailbox")
	errGoogleEmail    = errors.New("google sent an email inbok cannot store")
)

// Grant is what Google returns for an authorization code, as the adapter
// read and checked it.
type Grant struct {
	// RefreshToken is empty when Google sent none.
	RefreshToken string
	// AccessToken is used once, to check the mailbox, and never stored.
	AccessToken string
	// Identity comes from the ID token in the same response.
	Identity      Identity
	EmailVerified bool
	// GmailGranted is whether the member left Gmail access ticked.
	GmailGranted bool
}

// String returns g without its tokens, so printing a grant leaks neither.
func (g Grant) String() string {
	return fmt.Sprintf("Grant{Subject: %q, EmailVerified: %t, GmailGranted: %t}", g.Identity.Subject, g.EmailVerified, g.GmailGranted)
}

// Profile is the Gmail mailbox an access token reaches.
type Profile struct {
	Email string
}

// googleClient is Google's side of the flow.
type googleClient interface {
	// AuthCodeURL returns the consent page address for state, with a PKCE
	// challenge for verifier.
	AuthCodeURL(state, verifier string) string
	// Exchange trades code and its verifier for a grant. It returns an error
	// when Google refuses the code or its ID token fails a check.
	Exchange(ctx context.Context, code, verifier string) (Grant, error)
	// Profile returns the mailbox accessToken reaches.
	Profile(ctx context.Context, accessToken string) (Profile, error)
}

// ConnectState is one start of the flow, saved until its callback.
type ConnectState struct {
	StateHash [sha256.Size]byte
	UserID    int64
	SessionID uuid.UUID
	// AccountID is the account to reconnect; 0 connects a new one.
	AccountID    int64
	CodeVerifier string
	ExpiresAt    time.Time
}

// String returns s without its verifier.
func (s ConnectState) String() string {
	return fmt.Sprintf("ConnectState{UserID: %d, AccountID: %d, ExpiresAt: %s}", s.UserID, s.AccountID, s.ExpiresAt)
}

// connectStorage keeps connect states.
type connectStorage interface {
	// InsertConnectState saves state.
	InsertConnectState(ctx context.Context, state ConnectState) error
	// DeleteConnectState deletes the state saved under stateHash and returns
	// it, or ErrStateInvalid when there is none.
	DeleteConnectState(ctx context.Context, stateHash [sha256.Size]byte) (ConnectState, error)
}

// Callback is what Google's redirect brings back: the state, and either a
// code or an error.
type Callback struct {
	State string
	Code  string
	Error string
}

// Connections runs the connect flow: Start sends the member to Google, and
// Finish saves what Google sends back. It exists only when a Google client
// is configured.
type Connections struct {
	storage  connectStorage
	accounts *Service
	google   googleClient
	now      func() time.Time
}

// NewConnections returns the flow given storage, the account rules it saves
// with, a configured Google client, and a clock.
func NewConnections(storage connectStorage, accounts *Service, google googleClient, now func() time.Time) *Connections {
	return &Connections{storage: storage, accounts: accounts, google: google, now: now}
}

// Start saves a new state for userID's session sessionID and returns the
// address of Google's consent page. accountID names the account to
// reconnect, or is 0 to connect a new one. It returns ErrNotFound when
// userID has no account accountID.
func (c *Connections) Start(ctx context.Context, userID int64, sessionID uuid.UUID, accountID int64) (string, error) {
	if sessionID == (uuid.UUID{}) {
		return "", errNoSession
	}

	// 1. Refuse a reconnect of an account the member does not have, before
	// saving a state that names it. A disconnect after this check deletes
	// the state with the account.
	if accountID != 0 {
		accounts, err := c.accounts.List(ctx, userID)
		if err != nil {
			return "", err
		}
		if !hasAccount(accounts, accountID) {
			return "", ErrNotFound
		}
	}

	// 2. Save the state before the member can use it. The verifier is a
	// second random secret: base64url is a valid PKCE verifier.
	state, stateHash := randomsecret.New()
	verifier, _ := randomsecret.New()
	err := c.storage.InsertConnectState(ctx, ConnectState{
		StateHash:    stateHash,
		UserID:       userID,
		SessionID:    sessionID,
		AccountID:    accountID,
		CodeVerifier: verifier,
		ExpiresAt:    c.now().UTC().Add(stateLifetime),
	})
	if err != nil {
		return "", err
	}
	return c.google.AuthCodeURL(state, verifier), nil
}

// Finish ends the flow userID's session sessionID started with callback's
// state: it trades the code for a grant, checks it and the mailbox, and
// connects or reconnects the account, which it returns. The state works
// once, whatever the outcome.
//
// It returns ErrStateInvalid, ErrCancelled, ErrGmailNotGranted,
// ErrEmailNotVerified, ErrAlreadyConnected, ErrDifferentAccount, or
// ErrNotFound when the reconnected account went in the moment before the
// save; any other error is a failure the member cannot fix.
//
// DESIGN: a grant that is not saved is left alone at Google, not revoked.
// Revoking any token ends the Google account's whole grant to inbok's
// Google project, which another connection of the same account in the
// instance may be using (ADR006).
func (c *Connections) Finish(ctx context.Context, userID int64, sessionID uuid.UUID, callback Callback) (Account, error) {
	// 1. Take the state, so it cannot be used again whatever follows.
	hash, err := randomsecret.Hash(callback.State)
	if err != nil {
		return Account{}, ErrStateInvalid
	}
	state, err := c.storage.DeleteConnectState(ctx, hash)
	if err != nil {
		return Account{}, err
	}

	// 2. Refuse a state another session started, or one that expired, before
	// anything reaches Google: the code may be someone else's.
	if err := checkState(state, userID, sessionID, c.now()); err != nil {
		return Account{}, err
	}

	// 3. Refuse what Google refused, before trading a code that is not there.
	switch {
	case callback.Error == errorAccessDenied:
		return Account{}, ErrCancelled
	case callback.Error != "":
		return Account{}, errAuthorization
	case callback.Code == "":
		return Account{}, errNoCode
	}

	// 4. Trade the code. Only this state holds the verifier, so a code taken
	// from someone else's browser is useless here.
	grant, err := c.google.Exchange(ctx, callback.Code, state.CodeVerifier)
	if err != nil {
		return Account{}, fmt.Errorf("exchange code: %w", err)
	}

	// 5. Refuse a grant inbok cannot use before spending a Gmail call on it.
	switch {
	case grant.RefreshToken == "":
		return Account{}, errNoGrantToken
	case !grant.GmailGranted:
		return Account{}, ErrGmailNotGranted
	case !grant.EmailVerified:
		return Account{}, ErrEmailNotVerified
	}

	// 6. Confirm that the token reaches the Google account's own mailbox
	// before saving it.
	if err := c.validateMailbox(ctx, grant); err != nil {
		return Account{}, err
	}

	// 7. Save the account, or replace the reconnected one's token.
	account, err := c.save(ctx, userID, state.AccountID, grant)
	if errors.Is(err, emailaddress.ErrInvalid) {
		// The email came from Google; the member typed nothing to fix.
		return Account{}, errGoogleEmail
	}
	return account, err
}

// save connects grant as a new account of userID, or, when accountID is not
// 0, reconnects that account with it, and returns the account.
func (c *Connections) save(ctx context.Context, userID, accountID int64, grant Grant) (Account, error) {
	if accountID == 0 {
		return c.accounts.Connect(ctx, userID, grant.Identity, grant.RefreshToken)
	}
	return c.accounts.Reconnect(ctx, userID, accountID, grant.Identity, grant.RefreshToken)
}

// validateMailbox returns nil when grant's access token reaches the Gmail
// mailbox of grant's identity, and an error otherwise.
func (c *Connections) validateMailbox(ctx context.Context, grant Grant) error {
	profile, err := c.google.Profile(ctx, grant.AccessToken)
	if err != nil {
		return fmt.Errorf("read gmail profile: %w", err)
	}
	mailbox, mailboxErr := emailaddress.Parse(profile.Email)
	tokenEmail, tokenErr := emailaddress.Parse(grant.Identity.Email)
	if mailboxErr != nil || tokenErr != nil {
		return errGoogleEmail
	}
	if mailbox != tokenEmail {
		return errProfileMailbox
	}
	return nil
}

// checkState returns ErrStateInvalid unless state was started by userID's
// session sessionID and has not expired at now.
func checkState(state ConnectState, userID int64, sessionID uuid.UUID, now time.Time) error {
	if state.UserID != userID || state.SessionID != sessionID || !now.Before(state.ExpiresAt) {
		return ErrStateInvalid
	}
	return nil
}

// hasAccount reports whether accounts includes accountID.
func hasAccount(accounts []Account, accountID int64) bool {
	for _, account := range accounts {
		if account.ID == accountID {
			return true
		}
	}
	return false
}
