package google

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
)

func TestProfile(t *testing.T) {
	e, client := startEmulator(t)
	code, verifier := consent(t, e, client, "alex@example.com")
	grant, err := client.Exchange(t.Context(), code, verifier)
	require.NoError(t, err)

	profile, err := client.Profile(t.Context(), grant.AccessToken)
	require.NoError(t, err)

	assert.Equal(t, "alex@example.com", profile.Email)
}

func TestProfileRefusedToken(t *testing.T) {
	_, client := startEmulator(t)

	_, err := client.Profile(t.Context(), connectedaccount.NewAccessToken("not-a-token"))

	require.Error(t, err)
}

// A refusal from Gmail keeps its status and none of the body.
func TestProfileRefusalRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "secret page body", http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base}, time.Now)

	_, err = client.Profile(t.Context(), connectedaccount.NewAccessToken("access-1"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.NotContains(t, err.Error(), "secret page body")
	require.NotErrorIs(t, err, connectedaccount.ErrNoGmail)
}

// Gmail's answer for a Google account without Gmail is told apart from
// other refusals, still without the body.
func TestProfileNoGmail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Mail service not enabled",` +
			`"errors":[{"message":"Mail service not enabled","domain":"global","reason":"failedPrecondition"}],` +
			`"status":"FAILED_PRECONDITION"}}`))
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base}, time.Now)

	_, err = client.Profile(t.Context(), connectedaccount.NewAccessToken("access-1"))

	require.ErrorIs(t, err, connectedaccount.ErrNoGmail)
	assert.NotContains(t, err.Error(), "Mail service")
}
