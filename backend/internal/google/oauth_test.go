package google

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"amplop/internal/connectedaccount"
	"amplop/internal/randomsecret"
)

func TestAuthCodeURL(t *testing.T) {
	base, err := url.Parse("http://127.0.0.1:4000")
	require.NoError(t, err)
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base}, time.Now)

	consentURL, err := url.Parse(client.AuthCodeURL("state-1", "verifier-1"))
	require.NoError(t, err)

	assert.Equal(t, "http://127.0.0.1:4000/o/oauth2/v2/auth", consentURL.Scheme+"://"+consentURL.Host+consentURL.Path)
	query := consentURL.Query()
	assert.Equal(t, testClientID, query.Get("client_id"))
	assert.Equal(t, testRedirect, query.Get("redirect_uri"))
	assert.Equal(t, "code", query.Get("response_type"))
	assert.Equal(t, "openid email https://www.googleapis.com/auth/gmail.modify", query.Get("scope"))
	assert.Equal(t, "state-1", query.Get("state"))
	assert.Equal(t, "offline", query.Get("access_type"))
	assert.Equal(t, "consent", query.Get("prompt"))
	assert.Equal(t, "S256", query.Get("code_challenge_method"))
	assert.Equal(t, oauth2.S256ChallengeFromVerifier("verifier-1"), query.Get("code_challenge"))
}

// Without a base URL the client uses Google's own consent page.
func TestAuthCodeURLGoogle(t *testing.T) {
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect}, time.Now)

	consentURL, err := url.Parse(client.AuthCodeURL("state-1", "verifier-1"))
	require.NoError(t, err)

	assert.Equal(t, "https://accounts.google.com/o/oauth2/v2/auth", consentURL.Scheme+"://"+consentURL.Host+consentURL.Path)
}

func TestExchange(t *testing.T) {
	e, client := startEmulator(t)
	code, verifier := consent(t, e, client, "alex@example.com")

	grant, err := client.Exchange(t.Context(), code, verifier)
	require.NoError(t, err)

	assert.NotEmpty(t, grant.RefreshToken)
	assert.NotEmpty(t, grant.AccessToken.Value())
	assert.Equal(t, alexSubject, grant.Identity.Subject)
	assert.Equal(t, "alex@example.com", grant.Identity.Email)
	assert.True(t, grant.EmailVerified)
	assert.True(t, grant.GmailGranted)
}

func TestExchangeUnverified(t *testing.T) {
	e, client := startEmulator(t)
	code, verifier := consent(t, e, client, "unverified@example.com")

	grant, err := client.Exchange(t.Context(), code, verifier)
	require.NoError(t, err)

	assert.False(t, grant.EmailVerified)
}

// Google refuses a code sent with another verifier, so a code taken from
// the browser alone is useless.
func TestExchangeWrongVerifier(t *testing.T) {
	e, client := startEmulator(t)
	code, _ := consent(t, e, client, "alex@example.com")
	other, _ := randomsecret.New()

	_, err := client.Exchange(t.Context(), code, other)

	var refused *tokenRefusal
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "invalid_grant", refused.code)
	assert.Equal(t, http.StatusBadRequest, refused.status)
}

func TestExchangeGmailUnticked(t *testing.T) {
	now := testNow
	client := stubGoogle(t, now, func(issuer string) map[string]any {
		response := tokenResponse(t, alexClaims(issuer, now, nil))
		response["scope"] = "openid email"
		return response
	})

	grant, err := client.Exchange(t.Context(), "code-1", "verifier-1")
	require.NoError(t, err)

	assert.False(t, grant.GmailGranted)
}

func TestExchangeNoRefreshToken(t *testing.T) {
	now := testNow
	client := stubGoogle(t, now, func(issuer string) map[string]any {
		response := tokenResponse(t, alexClaims(issuer, now, nil))
		delete(response, "refresh_token")
		return response
	})

	grant, err := client.Exchange(t.Context(), "code-1", "verifier-1")
	require.NoError(t, err)

	assert.Empty(t, grant.RefreshToken)
}

func TestExchangeNoIDToken(t *testing.T) {
	now := testNow
	client := stubGoogle(t, now, func(issuer string) map[string]any {
		response := tokenResponse(t, alexClaims(issuer, now, nil))
		delete(response, "id_token")
		return response
	})

	_, err := client.Exchange(t.Context(), "code-1", "verifier-1")

	require.ErrorIs(t, err, errNoIDToken)
}

// A refusal from the token endpoint keeps its status and code, and none of
// the body, which may be any page.
func TestExchangeRefusalRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "<html>secret page body</html>", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base}, time.Now)

	_, err = client.Exchange(t.Context(), "code-1", "verifier-1")

	var refused *tokenRefusal
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, http.StatusBadGateway, refused.status)
	assert.NotContains(t, err.Error(), "secret page body")
}

func TestRefresh(t *testing.T) {
	e, client := startEmulator(t)
	code, verifier := consent(t, e, client, "alex@example.com")
	grant, err := client.Exchange(t.Context(), code, verifier)
	require.NoError(t, err)

	renewal, err := client.Refresh(t.Context(), grant.RefreshToken)
	require.NoError(t, err)

	assert.Empty(t, renewal.RefreshToken, "Google kept the refresh token")
	assert.False(t, renewal.Expiry.IsZero(), "Google says when the access token expires")
	profile, err := client.Profile(t.Context(), renewal.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "alex@example.com", profile.Email)
}

func TestRefreshRevoked(t *testing.T) {
	e, client := startEmulator(t)
	code, verifier := consent(t, e, client, "alex@example.com")
	grant, err := client.Exchange(t.Context(), code, verifier)
	require.NoError(t, err)
	e.Revoke(t, grant.RefreshToken)

	_, err = client.Refresh(t.Context(), grant.RefreshToken)

	require.ErrorIs(t, err, connectedaccount.ErrRefreshRefused)
}

// stubRefresh starts a token endpoint that answers every request with
// status and body, and returns a client pointed at it.
func stubRefresh(t *testing.T, status int, body map[string]any) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	return New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base}, time.Now)
}

func TestRefreshReplacement(t *testing.T) {
	client := stubRefresh(t, http.StatusOK, map[string]any{
		"access_token": "access-2", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "refresh-2",
	})

	renewal, err := client.Refresh(t.Context(), "refresh-1")
	require.NoError(t, err)

	assert.Equal(t, "access-2", renewal.AccessToken.Value())
	assert.Equal(t, "refresh-2", renewal.RefreshToken)
}

// The same refresh token sent back is no replacement.
func TestRefreshSameToken(t *testing.T) {
	client := stubRefresh(t, http.StatusOK, map[string]any{
		"access_token": "access-2", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "refresh-1",
	})

	renewal, err := client.Refresh(t.Context(), "refresh-1")
	require.NoError(t, err)

	assert.Empty(t, renewal.RefreshToken)
}

// Only invalid_grant is something the member's reconnect mends; the rest,
// e.g. a wrong client secret or Google failing, would mark every account,
// and an administrator's block outlasts any reconnect.
func TestRefreshOtherRefusals(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
	}{
		{"wrong client", http.StatusUnauthorized, "invalid_client"},
		{"grant type not allowed", http.StatusUnauthorized, "unauthorized_client"},
		{"client deleted", http.StatusUnauthorized, "deleted_client"},
		{"administrator block", http.StatusBadRequest, "admin_policy_enforced"},
		{"google failing", http.StatusServiceUnavailable, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := stubRefresh(t, test.status, map[string]any{"error": test.code, "error_description": "secret page body"})

			_, err := client.Refresh(t.Context(), "refresh-1")

			var refused *tokenRefusal
			require.ErrorAs(t, err, &refused)
			assert.Equal(t, test.status, refused.status)
			require.NotErrorIs(t, err, connectedaccount.ErrRefreshRefused)
			assert.NotContains(t, err.Error(), "secret page body")
		})
	}
}

// A revoked refresh token is refused afterwards, and revoking it again
// still succeeds.
func TestRevoke(t *testing.T) {
	e, client := startEmulator(t)
	code, verifier := consent(t, e, client, "alex@example.com")
	grant, err := client.Exchange(t.Context(), code, verifier)
	require.NoError(t, err)

	require.NoError(t, client.Revoke(t.Context(), grant.RefreshToken))

	_, err = client.Refresh(t.Context(), grant.RefreshToken)
	require.ErrorIs(t, err, connectedaccount.ErrRefreshRefused)
	require.NoError(t, client.Revoke(t.Context(), grant.RefreshToken))
}

// stubRevoke starts a revocation endpoint that answers every request with
// status and body, and records the last request's query and form. It
// returns a client pointed at it.
func stubRevoke(t *testing.T, status int, body map[string]any) (*Client, *http.Request) {
	t.Helper()
	got := &http.Request{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/revoke" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		*got = *r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	return New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base}, time.Now), got
}

// The token travels in the body, never the URL.
func TestRevokeSendsTokenInBody(t *testing.T) {
	client, got := stubRevoke(t, http.StatusOK, map[string]any{})

	require.NoError(t, client.Revoke(t.Context(), "refresh-1"))

	assert.Equal(t, "refresh-1", got.PostForm.Get("token"))
	assert.Empty(t, got.URL.RawQuery)
}

// Google answers 400 invalid_token for a token it no longer knows, which
// is as good as revoked.
func TestRevokeUnknownToken(t *testing.T) {
	client, _ := stubRevoke(t, http.StatusBadRequest, map[string]any{"error": "invalid_token", "error_description": "Token expired or revoked"})

	require.NoError(t, client.Revoke(t.Context(), "refresh-1"))
}

// Any other refusal fails with its status and code, and none of the body.
func TestRevokeRefused(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
	}{
		{"bad request", http.StatusBadRequest, "invalid_request"},
		{"google failing", http.StatusServiceUnavailable, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, _ := stubRevoke(t, test.status, map[string]any{"error": test.code, "error_description": "secret page body"})

			err := client.Revoke(t.Context(), "refresh-1")

			var refused *tokenRefusal
			require.ErrorAs(t, err, &refused)
			assert.Equal(t, test.status, refused.status)
			assert.Equal(t, test.code, refused.code)
			assert.NotContains(t, err.Error(), "secret page body")
			assert.NotContains(t, err.Error(), "refresh-1")
		})
	}
}
