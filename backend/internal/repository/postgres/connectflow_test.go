package postgres

import (
	"net/url"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/google"
	"inbok/internal/googleemulator"
)

const (
	flowClientID = "inbok-test.apps.googleusercontent.com"
	flowSecret   = "GOCSPX-test"
	flowRedirect = "http://localhost:5173/api/v0/connected-accounts/google/callback"
)

// connectFlowTest is the whole flow and the token access after it:
// PostgreSQL, the Google emulator with alex and blair, and a member signed
// in.
type connectFlowTest struct {
	connectedAccountsTest
	emulator    *googleemulator.Emulator
	google      *google.Client
	connections *connectedaccount.Connections
	access      *connectedaccount.Access
	session     uuid.UUID
}

func newConnectFlowTest(t *testing.T) connectFlowTest {
	t.Helper()
	c := newConnectedAccountsTest(t)
	emulator := googleemulator.Start(t, googleemulator.Seed{
		Users: []googleemulator.User{
			{Email: alexIdentity.Email, Name: "Alex Rivera", UID: alexIdentity.Subject},
			{Email: blairIdentity.Email, Name: "Blair Chen", UID: blairIdentity.Subject},
		},
		Clients: []googleemulator.Client{{ID: flowClientID, Secret: flowSecret, RedirectURIs: []string{flowRedirect}}},
	})
	client := google.New(google.Settings{
		ClientID: flowClientID, ClientSecret: flowSecret, RedirectURL: flowRedirect, BaseURL: emulator.BaseURL,
	}, time.Now)
	return connectFlowTest{
		connectedAccountsTest: c,
		emulator:              emulator,
		google:                client,
		connections:           connectedaccount.NewConnections(c.db, c.service, client, time.Now),
		access:                connectedaccount.NewAccess(c.db, c.ring, client, time.Now),
		session:               insertConnectingSession(t, c.conn, c.member),
	}
}

// consent starts the flow for accountID, agrees on the emulator's consent
// page as email, and finishes with what it sends back.
func (f connectFlowTest) consent(t *testing.T, accountID int64, email string) (connectedaccount.Account, error) {
	t.Helper()
	page, err := f.connections.Start(t.Context(), f.member, f.session, accountID)
	require.NoError(t, err)
	back := f.emulator.Consent(t, page, email).Query()
	return f.connections.Finish(t.Context(), f.member, f.session, connectedaccount.Callback{
		State: back.Get("state"), Code: back.Get("code"), Error: back.Get("error"),
	})
}

func TestConnectFlow(t *testing.T) {
	f := newConnectFlowTest(t)

	account, err := f.consent(t, 0, alexIdentity.Email)
	require.NoError(t, err)

	assert.Equal(t, alexIdentity.Email, account.Email)
	assert.Equal(t, alexIdentity.Subject, account.Subject)
	assert.NotEmpty(t, f.storedToken(t, f.member, account.ID))
	assert.Equal(t, int64(0), countConnectStates(t, f.conn))
}

func TestConnectFlowReconnect(t *testing.T) {
	f := newConnectFlowTest(t)
	account, err := f.consent(t, 0, alexIdentity.Email)
	require.NoError(t, err)
	before := f.storedToken(t, f.member, account.ID)

	reconnected, err := f.consent(t, account.ID, alexIdentity.Email)
	require.NoError(t, err)

	assert.Equal(t, account.ID, reconnected.ID)
	assert.NotEqual(t, before, f.storedToken(t, f.member, account.ID), "the new refresh token replaces the old")
}

func TestConnectFlowReconnectDifferentAccount(t *testing.T) {
	f := newConnectFlowTest(t)
	account, err := f.consent(t, 0, alexIdentity.Email)
	require.NoError(t, err)
	before := f.storedToken(t, f.member, account.ID)

	_, err = f.consent(t, account.ID, blairIdentity.Email)

	require.ErrorIs(t, err, connectedaccount.ErrDifferentAccount)
	assert.Equal(t, before, f.storedToken(t, f.member, account.ID))
}

func TestConnectFlowAlreadyConnected(t *testing.T) {
	f := newConnectFlowTest(t)
	_, err := f.consent(t, 0, alexIdentity.Email)
	require.NoError(t, err)

	_, err = f.consent(t, 0, alexIdentity.Email)

	require.ErrorIs(t, err, connectedaccount.ErrAlreadyConnected)
	assert.Equal(t, int64(1), f.countConnectedAccounts(t))
}

// The emulator's page cannot decline, so the redirect Google sends then is
// given directly.
func TestConnectFlowCancelled(t *testing.T) {
	f := newConnectFlowTest(t)
	page, err := f.connections.Start(t.Context(), f.member, f.session, 0)
	require.NoError(t, err)
	consentURL, err := url.Parse(page)
	require.NoError(t, err)
	state := consentURL.Query().Get("state")

	_, err = f.connections.Finish(t.Context(), f.member, f.session, connectedaccount.Callback{State: state, Error: "access_denied"})

	require.ErrorIs(t, err, connectedaccount.ErrCancelled)
	assert.Equal(t, int64(0), countConnectStates(t, f.conn))
}

// A job's token reaches the connected mailbox.
func TestConnectFlowToken(t *testing.T) {
	f := newConnectFlowTest(t)
	account, err := f.consent(t, 0, alexIdentity.Email)
	require.NoError(t, err)

	token, err := f.access.Token(t.Context(), account.ID)
	require.NoError(t, err)

	profile, err := f.google.Profile(t.Context(), token)
	require.NoError(t, err)
	assert.Equal(t, alexIdentity.Email, profile.Email)
}

func TestConnectFlowTokenRevoked(t *testing.T) {
	// Given a connected account whose member removed inbok's access at
	// Google.
	f := newConnectFlowTest(t)
	account, err := f.consent(t, 0, alexIdentity.Email)
	require.NoError(t, err)
	f.emulator.Revoke(t, f.storedToken(t, f.member, account.ID))

	// When a job asks for a token.
	_, err = f.access.Token(t.Context(), account.ID)

	// Then the account needs reconnecting, and reconnecting mends it.
	require.ErrorIs(t, err, connectedaccount.ErrNeedsReconnect)
	accounts, err := f.service.List(t.Context(), f.member)
	require.NoError(t, err)
	assert.NotNil(t, accounts[0].NeedsReconnectSince)
	_, err = f.consent(t, account.ID, alexIdentity.Email)
	require.NoError(t, err)
	_, err = f.access.Token(t.Context(), account.ID)
	require.NoError(t, err)
}
