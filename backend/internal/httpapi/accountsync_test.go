package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/account"
	"amplop/internal/attemptlimit"
	"amplop/internal/connectedaccount"
	"amplop/internal/mailsync"
)

// fakeSyncs answers with reports by account, stopped, and requestErr, and
// records what it was asked.
type fakeSyncs struct {
	reports    map[int64]mailsync.Report
	stopped    bool
	requestErr error

	read              []int64
	userID, accountID int64
	requests          int
}

func (f *fakeSyncs) Reports(_ context.Context, accountIDs []int64) ([]mailsync.Report, error) {
	f.read = accountIDs
	var reports []mailsync.Report
	for _, id := range accountIDs {
		if report, ok := f.reports[id]; ok {
			reports = append(reports, report)
		}
	}
	return reports, nil
}

func (f *fakeSyncs) Stopped(context.Context) (bool, error) {
	return f.stopped, nil
}

func (f *fakeSyncs) Request(_ context.Context, userID, accountID int64) (mailsync.Report, error) {
	f.requests++
	f.userID, f.accountID = userID, accountID
	if f.requestErr != nil {
		return mailsync.Report{}, f.requestErr
	}
	return f.reports[accountID], nil
}

// A request starts the member's account's sync and answers 202 with where
// it stands.
func TestSyncAccount(t *testing.T) {
	syncs := &fakeSyncs{reports: map[int64]mailsync.Report{
		3: {AccountID: 3, Status: mailsync.StatusComplete, LastSyncedAt: &alexCreated, RequestedAt: &alexRefused},
	}}
	api := New(discard, Services{Connections: &fakeConnections{}, Syncs: syncs, Session: memberSessions(account.RoleMember)})

	response := requestAuthenticated(api, http.MethodPost, "/connected-accounts/3/sync", "")

	require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"status":"complete","messagesSoFar":null,"lastSyncedAt":"2026-11-05T09:00:00Z",`+
		`"failure":null,"failedAt":null}}`, response.Body.String())
	assert.Equal(t, int64(42), syncs.userID)
	assert.Equal(t, int64(3), syncs.accountID)
}

func TestSyncAccountRefused(t *testing.T) {
	for _, c := range []struct {
		name       string
		err        error
		status     int
		typ        string
		retryAfter string
	}{
		{"not the member's", mailsync.ErrNotFound, http.StatusNotFound, connectedAccountNotFoundType, ""},
		{"needs reconnecting", connectedaccount.ErrNeedsReconnect, http.StatusConflict, accountNeedsReconnectType, ""},
		{"requested moments ago", &attemptlimit.Error{Err: mailsync.ErrRequestThrottled, Wait: 14*time.Second + time.Millisecond},
			http.StatusTooManyRequests, syncThrottledType, "15"},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := New(discard, Services{Connections: &fakeConnections{}, Syncs: &fakeSyncs{requestErr: c.err}, Session: memberSessions(account.RoleMember)})

			response := requestAuthenticated(api, http.MethodPost, "/connected-accounts/3/sync", "")

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Equal(t, c.typ, decodeJSON(t, response.Body.Bytes())["type"])
			assert.Equal(t, c.retryAfter, response.Header().Get(retryAfterHeader))
		})
	}
}

// Without a Google client no worker syncs Gmail, so a request is refused
// before the service queues a sync that would wait forever.
func TestSyncAccountNotConfigured(t *testing.T) {
	syncs := &fakeSyncs{}
	api := New(discard, Services{Syncs: syncs, Session: memberSessions(account.RoleMember)})

	response := requestAuthenticated(api, http.MethodPost, "/connected-accounts/3/sync", "")

	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	assert.Equal(t, gmailNotConfiguredType, decodeJSON(t, response.Body.Bytes())["type"])
	assert.Zero(t, syncs.requests)
}

// A request from another site is refused before the service.
func TestSyncAccountCrossOrigin(t *testing.T) {
	syncs := &fakeSyncs{}
	api := New(discard, Services{Syncs: syncs, Session: memberSessions(account.RoleMember)})
	request := httptest.NewRequest(http.MethodPost, "/api/v0/connected-accounts/3/sync", nil)
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	assert.Zero(t, syncs.requests)
}

// The contract allows a null failure, which every healthy account sends.
func TestAccountSyncSchema(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()

	failure := doc.Components.Schemas.Map()["AccountSync"].Properties["failure"]

	assert.Equal(t, []any{"provider", "token-unreadable", "internal", nil}, failure.Enum)
}

// The contract lists every action a listed account can allow.
func TestListedAccountSchema(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()

	actions := doc.Components.Schemas.Map()["ListedAccount"].Properties["allowedActions"]

	assert.Equal(t, []any{"sync", "send", "change-mail", "reconnect", "rename", "disconnect"}, actions.Items.Enum)
}
