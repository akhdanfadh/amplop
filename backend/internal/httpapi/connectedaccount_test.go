package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/attemptlimit"
	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
	"inbok/internal/session"
)

type fakeConnectedAccounts struct {
	accounts []connectedaccount.Account
	err      error

	userID, accountID int64
	calls             int
}

func (f *fakeConnectedAccounts) List(_ context.Context, userID int64) ([]connectedaccount.Account, error) {
	f.calls++
	f.userID = userID
	return f.accounts, f.err
}

func (f *fakeConnectedAccounts) Disconnect(_ context.Context, userID, accountID int64) (connectedaccount.Account, error) {
	f.calls++
	f.userID, f.accountID = userID, accountID
	if f.err != nil {
		return connectedaccount.Account{}, f.err
	}
	return f.accounts[0], nil
}

type fakeConnections struct {
	startErr  error
	finishErr error

	userID, accountID int64
	sessionID         uuid.UUID
	callback          connectedaccount.Callback
	starts, finishes  int
}

func (f *fakeConnections) Start(_ context.Context, userID int64, sessionID uuid.UUID, accountID int64) (string, error) {
	f.starts++
	f.userID, f.sessionID, f.accountID = userID, sessionID, accountID
	return "https://accounts.google.com/o/oauth2/v2/auth?state=s1", f.startErr
}

func (f *fakeConnections) Finish(_ context.Context, userID int64, sessionID uuid.UUID, callback connectedaccount.Callback) (connectedaccount.Account, error) {
	f.finishes++
	f.userID, f.sessionID, f.callback = userID, sessionID, callback
	return connectedaccount.Account{}, f.finishErr
}

var (
	alexCreated = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	alexRefused = time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	alexAccount = connectedaccount.Account{
		ID: 3, UserID: 42, Provider: connectedaccount.ProviderGmail, Email: "alex@example.com",
		Subject: "100000000000000000001", CreatedAt: alexCreated, Status: connectedaccount.StatusUsable,
	}
)

const alexJSON = `{"id":3,"provider":"gmail","email":"alex@example.com","needsReconnectSince":null,"status":"usable","createdAt":"2026-11-05T09:00:00Z"}`

// TestGoogleCallbackURL: Google's client registers the callback under the
// public URL, at the path the setup guide names.
func TestGoogleCallbackURL(t *testing.T) {
	public, err := url.Parse("https://mail.example.com")
	require.NoError(t, err)

	assert.Equal(t, "https://mail.example.com/api/v0/connected-accounts/google/callback", GoogleCallbackURL(public))
}

// ========================
// Connected account list
// ========================

// The list shows each account with its sync, and never Google's ID for
// it.
func TestListConnectedAccounts(t *testing.T) {
	refused := alexAccount
	refused.NeedsReconnectSince = &alexRefused
	refused.Status = connectedaccount.StatusNeedsReconnect
	unreadable := alexAccount
	unreadable.Status = connectedaccount.StatusTokenUnreadable
	const waitingJSON = `"sync":{"status":"waiting","messagesSoFar":null,"lastSyncedAt":null,"failure":null,"failedAt":null}`
	for _, c := range []struct {
		name     string
		accounts []connectedaccount.Account
		stopped  bool
		want     string
	}{
		{"none", []connectedaccount.Account{}, false, `{"data":{"accounts":[],"syncStopped":false}}`},
		{"one", []connectedaccount.Account{alexAccount}, false,
			`{"data":{"accounts":[` + alexJSON[:len(alexJSON)-1] + `,` + waitingJSON + `}],"syncStopped":false}}`},
		{"needs reconnecting", []connectedaccount.Account{refused}, false,
			`{"data":{"accounts":[{"id":3,"provider":"gmail","email":"alex@example.com",` +
				`"needsReconnectSince":"2026-11-06T09:00:00Z","status":"needs-reconnect","createdAt":"2026-11-05T09:00:00Z",` + waitingJSON + `}],"syncStopped":false}}`},
		{"token unreadable", []connectedaccount.Account{unreadable}, false,
			`{"data":{"accounts":[{"id":3,"provider":"gmail","email":"alex@example.com",` +
				`"needsReconnectSince":null,"status":"token-unreadable","createdAt":"2026-11-05T09:00:00Z",` + waitingJSON + `}],"syncStopped":false}}`},
		{"syncing stopped", []connectedaccount.Account{}, true, `{"data":{"accounts":[],"syncStopped":true}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			accounts := &fakeConnectedAccounts{accounts: c.accounts}
			syncs := &fakeSyncs{reports: map[int64]mailsync.Report{3: {AccountID: 3, Status: mailsync.StatusWaiting}}, stopped: c.stopped}
			api := New(discard, Services{ConnectedAccounts: accounts, Syncs: syncs, Session: memberSessions(account.RoleMember)})

			response := requestAuthenticated(api, http.MethodGet, "/connected-accounts", "")

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, c.want, response.Body.String())
			assert.NotContains(t, response.Body.String(), "100000000000000000001")
			assert.Equal(t, int64(42), accounts.userID)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		})
	}
}

// The list reads only the member's accounts' sync states, and leaves out
// an account disconnected between the reads.
func TestListConnectedAccountsSync(t *testing.T) {
	blair := alexAccount
	blair.ID, blair.Email = 4, "blair@example.com"
	accounts := &fakeConnectedAccounts{accounts: []connectedaccount.Account{alexAccount, blair}}
	syncs := &fakeSyncs{reports: map[int64]mailsync.Report{
		3: {AccountID: 3, Status: mailsync.StatusRunning, MessagesSoFar: 250, FailedAt: &alexRefused, Failure: mailsync.FailureProvider, LastSyncedAt: &alexCreated, RequestedAt: &alexRefused},
	}}
	api := New(discard, Services{ConnectedAccounts: accounts, Syncs: syncs, Session: memberSessions(account.RoleMember)})

	response := requestAuthenticated(api, http.MethodGet, "/connected-accounts", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, []int64{3, 4}, syncs.read)
	listed := decodeJSON(t, response.Body.Bytes())["data"].(map[string]any)["accounts"].([]any)
	require.Len(t, listed, 1)
	assert.Equal(t, map[string]any{
		"status": "running", "messagesSoFar": float64(250), "lastSyncedAt": "2026-11-05T09:00:00Z",
		"failure": "provider", "failedAt": "2026-11-06T09:00:00Z",
	}, listed[0].(map[string]any)["sync"])
}

// ========================
// Account providers
// ========================

// Gmail is offered only while a Google client is configured.
func TestListAccountProviders(t *testing.T) {
	for _, c := range []struct {
		name        string
		connections connectionService
		want        string
	}{
		{"Google configured", &fakeConnections{}, `{"data":{"offered":["gmail"]}}`},
		{"not configured", nil, `{"data":{"offered":[]}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := New(discard, Services{Connections: c.connections, Session: memberSessions(account.RoleMember)})

			response := requestAuthenticated(api, http.MethodGet, "/connected-accounts/providers", "")

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, c.want, response.Body.String())
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		})
	}
}

// Only signed-in accounts see what the instance offers.
func TestListAccountProvidersSignedOut(t *testing.T) {
	api := New(discard, Services{Connections: &fakeConnections{}, Session: &fakeSessions{currentErr: session.ErrUnauthenticated}})

	response := requestAuthenticated(api, http.MethodGet, "/connected-accounts/providers", "")

	require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
}

// ========================
// Gmail connect and reconnect
// ========================

// A start is for the signed-in account and its session, and returns
// Google's consent page.
func TestConnectGmail(t *testing.T) {
	for _, c := range []struct {
		name      string
		path      string
		accountID int64
	}{
		{"connect", "/connected-accounts/google", 0},
		{"reconnect", "/connected-accounts/3/reconnect", 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			sessions := memberSessions(account.RoleMember)
			sessions.reference = checkedReference(t)
			require.NotEqual(t, uuid.UUID{}, sessions.reference.ID())
			connections := &fakeConnections{}
			api := New(discard, Services{Connections: connections, Session: sessions})

			response := requestAuthenticated(api, http.MethodPost, c.path, "")

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, `{"data":{"url":"https://accounts.google.com/o/oauth2/v2/auth?state=s1"}}`, response.Body.String())
			assert.Equal(t, int64(42), connections.userID)
			assert.Equal(t, sessions.reference.ID(), connections.sessionID)
			assert.Equal(t, c.accountID, connections.accountID)
		})
	}
}

func TestConnectGmailRefused(t *testing.T) {
	throttled := &attemptlimit.Error{Err: connectedaccount.ErrStartThrottled, Wait: 90 * time.Second}
	for _, c := range []struct {
		name       string
		path       string
		err        error
		configured bool
		code       int
		typ        string
		retryAfter string
	}{
		{"not configured", "/connected-accounts/google", nil, false, http.StatusConflict, gmailNotConfiguredType, ""},
		{"reconnect not configured", "/connected-accounts/3/reconnect", nil, false, http.StatusConflict, gmailNotConfiguredType, ""},
		{"throttled", "/connected-accounts/google", throttled, true, http.StatusTooManyRequests, connectThrottledType, "90"},
		{"other member's account", "/connected-accounts/3/reconnect", connectedaccount.ErrNotFound, true, http.StatusNotFound, connectedAccountNotFoundType, ""},
		{"deactivated meanwhile", "/connected-accounts/google", connectedaccount.ErrDeactivated, true, http.StatusUnauthorized, unauthenticatedType, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			services := Services{Session: memberSessions(account.RoleMember)}
			connections := &fakeConnections{startErr: c.err}
			if c.configured {
				services.Connections = connections
			}
			api := New(discard, services)

			response := requestAuthenticated(api, http.MethodPost, c.path, "")

			require.Equal(t, c.code, response.Code, response.Body.String())
			assert.Equal(t, c.typ, decodeJSON(t, response.Body.Bytes())["type"])
			assert.Equal(t, c.retryAfter, response.Header().Get(retryAfterHeader))
		})
	}
}

func TestReconnectInvalidID(t *testing.T) {
	connections := &fakeConnections{}
	api := New(discard, Services{Connections: connections, Session: memberSessions(account.RoleMember)})

	response := requestAuthenticated(api, http.MethodPost, "/connected-accounts/0/reconnect", "")

	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	assert.Zero(t, connections.starts)
}

// ========================
// Google callback
// ========================

// callbackQuery is a callback's query string with a state and code that
// must never reach the logs.
const callbackQuery = "?state=state-secret-1&code=code-secret-1&scope=openid"

// requestCallback sends Google's redirect as a browser does: a top-level
// GET from another site, with the session cookie when secret is not empty.
func requestCallback(api *API, query, secret string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/v0/connected-accounts/google/callback"+query, nil)
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	request.Header.Set("Sec-Fetch-Mode", "navigate")
	if secret != "" {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: secret})
	}
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

// assertLanding checks that response redirects to the landing route with
// outcome, uncached and without a body.
func assertLanding(t *testing.T, response *httptest.ResponseRecorder, outcome connectOutcome) {
	t.Helper()
	require.Equal(t, http.StatusSeeOther, response.Code, response.Body.String())
	assert.Equal(t, "/connected-accounts?outcome="+string(outcome), response.Header().Get("Location"))
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Empty(t, response.Body.String())
}

// Google's redirect from another site reaches the flow with the session's
// account and ID, and lands on the outcome.
func TestGoogleCallback(t *testing.T) {
	sessions := memberSessions(account.RoleMember)
	sessions.reference = checkedReference(t)
	require.NotEqual(t, uuid.UUID{}, sessions.reference.ID())
	connections := &fakeConnections{}
	api := New(discard, Services{Connections: connections, Session: sessions})

	response := requestCallback(api, callbackQuery, "session-secret")

	assertLanding(t, response, outcomeConnected)
	assert.Equal(t, "session-secret", sessions.currentSecret)
	assert.Equal(t, int64(42), connections.userID)
	assert.Equal(t, sessions.reference.ID(), connections.sessionID)
	assert.Equal(t, connectedaccount.Callback{State: "state-secret-1", Code: "code-secret-1"}, connections.callback)
}

// Every way the flow ends lands on its outcome; failures the member cannot
// fix are logged, without the code, state, or query string.
func TestGoogleCallbackOutcomes(t *testing.T) {
	for _, c := range []struct {
		err     error
		outcome connectOutcome
	}{
		{connectedaccount.ErrStateInvalid, outcomeExpired},
		{connectedaccount.ErrCancelled, outcomeCancelled},
		{connectedaccount.ErrGmailNotGranted, outcomeGmailNotGranted},
		{connectedaccount.ErrEmailNotVerified, outcomeEmailNotVerified},
		{fmt.Errorf("check mailbox: %w", connectedaccount.ErrNoGmail), outcomeNoGmail},
		{connectedaccount.ErrAlreadyConnected, outcomeAlreadyConnected},
		{connectedaccount.ErrDifferentAccount, outcomeDifferentAccount},
		{connectedaccount.ErrNotFound, outcomeFailed},
		{errors.New("exchange code: google oauth endpoint: 500"), outcomeFailed},
	} {
		t.Run(string(c.outcome)+" "+c.err.Error(), func(t *testing.T) {
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{
				Connections: &fakeConnections{finishErr: c.err}, Session: memberSessions(account.RoleMember),
			})

			response := requestCallback(api, callbackQuery, "session-secret")

			assertLanding(t, response, c.outcome)
			if c.outcome == outcomeFailed {
				assert.Contains(t, logs.String(), "gmail connect failed")
			}
			for _, secret := range []string{"state-secret-1", "code-secret-1", "scope=openid"} {
				assert.NotContains(t, logs.String(), secret)
			}
		})
	}
}

// Without a usable session the state is still taken, by no one's session,
// so it fails the flow's check and cannot be used again.
func TestGoogleCallbackNoSession(t *testing.T) {
	for _, c := range []struct {
		name     string
		secret   string
		sessions *fakeSessions
	}{
		{"no cookie", "", memberSessions(account.RoleMember)},
		{"ended session", "session-secret", &fakeSessions{currentErr: session.ErrUnauthenticated}},
		{"no member access", "session-secret", &fakeSessions{identity: session.Identity{User: account.User{ID: 42, Role: account.RoleMember}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			connections := &fakeConnections{finishErr: connectedaccount.ErrStateInvalid}
			api := New(discard, Services{Connections: connections, Session: c.sessions})

			response := requestCallback(api, callbackQuery, c.secret)

			assertLanding(t, response, outcomeExpired)
			assert.Equal(t, 1, connections.finishes)
			assert.Zero(t, connections.userID)
			assert.Equal(t, uuid.UUID{}, connections.sessionID)
		})
	}
}

// A session check that fails lands on a failure, logged, and still takes
// the state, by no one's session, so a later visit cannot use it.
func TestCallbackSessionFails(t *testing.T) {
	var logs bytes.Buffer
	connections := &fakeConnections{finishErr: connectedaccount.ErrStateInvalid}
	api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{
		Connections: connections, Session: &fakeSessions{currentErr: errors.New("database unavailable")},
	})

	response := requestCallback(api, callbackQuery, "session-secret")

	assertLanding(t, response, outcomeFailed)
	assert.Equal(t, 1, connections.finishes)
	assert.Zero(t, connections.userID)
	assert.Equal(t, uuid.UUID{}, connections.sessionID)
	assert.Contains(t, logs.String(), "database unavailable")
}

// With no Google client configured, a stray callback lands on a failure.
func TestCallbackNotConfigured(t *testing.T) {
	api := New(discard, Services{Session: memberSessions(account.RoleMember)})

	response := requestCallback(api, callbackQuery, "session-secret")

	assertLanding(t, response, outcomeFailed)
}

// A cancel at Google brings an error and no code.
func TestGoogleCallbackCancelled(t *testing.T) {
	connections := &fakeConnections{finishErr: connectedaccount.ErrCancelled}
	api := New(discard, Services{Connections: connections, Session: memberSessions(account.RoleMember)})

	response := requestCallback(api, "?error=access_denied&state=state-secret-1", "session-secret")

	assertLanding(t, response, outcomeCancelled)
	assert.Equal(t, connectedaccount.Callback{State: "state-secret-1", Error: "access_denied"}, connections.callback)
}

// ========================
// Connected account disconnect
// ========================

func TestDisconnectAccount(t *testing.T) {
	accounts := &fakeConnectedAccounts{accounts: []connectedaccount.Account{alexAccount}}
	api := New(discard, Services{ConnectedAccounts: accounts, Session: memberSessions(account.RoleMember)})

	response := requestAuthenticated(api, http.MethodDelete, "/connected-accounts/3", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"account":`+alexJSON+`}}`, response.Body.String())
	assert.Equal(t, int64(42), accounts.userID)
	assert.Equal(t, int64(3), accounts.accountID)
}

// Another member's account is answered as a missing one.
func TestDisconnectAccountNotFound(t *testing.T) {
	accounts := &fakeConnectedAccounts{err: connectedaccount.ErrNotFound}
	api := New(discard, Services{ConnectedAccounts: accounts, Session: memberSessions(account.RoleMember)})

	response := requestAuthenticated(api, http.MethodDelete, "/connected-accounts/3", "")

	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	assert.Equal(t, connectedAccountNotFoundType, decodeJSON(t, response.Body.Bytes())["type"])
}

// A disconnect from another site is refused before the service.
func TestDisconnectCrossOrigin(t *testing.T) {
	accounts := &fakeConnectedAccounts{accounts: []connectedaccount.Account{alexAccount}}
	api := New(discard, Services{ConnectedAccounts: accounts, Session: memberSessions(account.RoleMember)})
	request := httptest.NewRequest(http.MethodDelete, "/api/v0/connected-accounts/3", nil)
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	assert.Zero(t, accounts.calls)
}

// The contract lists every outcome the landing route reads.
func TestConnectOutcomeSchema(t *testing.T) {
	doc, err := New(discard, Services{}).OpenAPI()
	require.NoError(t, err)

	assert.Contains(t, string(doc), `"ConnectOutcome"`)
	for _, outcome := range connectOutcomes {
		assert.Contains(t, string(doc), `"`+string(outcome)+`"`)
	}
}
