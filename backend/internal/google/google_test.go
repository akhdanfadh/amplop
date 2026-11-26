package google

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"inbok/internal/googleemulator"
	"inbok/internal/randomsecret"
)

// testNow is the clock stub clients read.
var testNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

const (
	testClientID = "inbok-test.apps.googleusercontent.com"
	testSecret   = "test-secret"
	testRedirect = "http://localhost:5173/api/v0/connected-accounts/google/callback"
	alexSubject  = "100000000000000000001"
)

// startEmulator starts an emulator with alex, an unverified account, and
// inbok's client, and returns a client pointed at it.
func startEmulator(t *testing.T) (*googleemulator.Emulator, *Client) {
	t.Helper()
	e := googleemulator.Start(t, googleemulator.Seed{
		Users: []googleemulator.User{
			{Email: "alex@example.com", Name: "Alex Rivera", UID: alexSubject},
			{Email: "unverified@example.com", Name: "Unverified", Unverified: true},
		},
		Clients: []googleemulator.Client{{ID: testClientID, Secret: testSecret, RedirectURIs: []string{testRedirect}}},
		Messages: []googleemulator.Message{
			{ID: "m1", Mailbox: "alex@example.com", From: "blair@example.com", Subject: "Hello", Body: "Hi."},
		},
	})
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: e.BaseURL}, time.Now)
	return e, client
}

// consent runs the consent page for email and returns the code Google sends
// back, with the verifier it was asked for with.
func consent(t *testing.T, e *googleemulator.Emulator, client *Client, email string) (string, string) {
	t.Helper()
	state, _ := randomsecret.New()
	verifier, _ := randomsecret.New()
	back := e.Consent(t, client.AuthCodeURL(state, verifier), email)
	require.Equal(t, state, back.Query().Get("state"))
	return back.Query().Get("code"), verifier
}

// idToken returns an unsigned JWT with claims, as a token response carries
// it.
func idToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// stubGoogle starts a token endpoint on this machine and returns a client
// pointed at it, whose clock reads now. The endpoint answers every exchange
// with respond's result, given the issuer the client expects, which is known
// only once the stub runs.
func stubGoogle(t *testing.T, now time.Time, respond func(issuer string) map[string]any) *Client {
	t.Helper()
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(respond(issuer))
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base}, func() time.Time { return now })
	issuer = client.issuers[0]
	return client
}

// alexClaims returns valid ID token claims for alex from issuer, with
// changes applied; a nil value removes the claim.
func alexClaims(issuer string, now time.Time, changes map[string]any) map[string]any {
	claims := map[string]any{
		"iss": issuer, "aud": testClientID, "sub": alexSubject, "email": "alex@example.com",
		"email_verified": true, "exp": now.Add(time.Hour).Unix(),
	}
	for name, value := range changes {
		if value == nil {
			delete(claims, name)
			continue
		}
		claims[name] = value
	}
	return claims
}

// tokenResponse returns a successful token response carrying [idClaims].
func tokenResponse(t *testing.T, idClaims map[string]any) map[string]any {
	t.Helper()
	return map[string]any{
		"access_token": "access-1", "token_type": "Bearer", "expires_in": 3600,
		"refresh_token": "refresh-1", "scope": "openid email https://www.googleapis.com/auth/gmail.modify",
		"id_token": idToken(t, idClaims),
	}
}
