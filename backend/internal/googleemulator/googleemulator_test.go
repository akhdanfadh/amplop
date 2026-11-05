package googleemulator

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	testClientID = "inbok-test.apps.googleusercontent.com"
	testSecret   = "GOCSPX-test"
	testRedirect = "http://localhost:5173/api/v0/connected-accounts/google/callback"
	testScopes   = "openid email https://www.googleapis.com/auth/gmail.modify"
)

// These tests check that the image behaves as inbok relies on, with the
// libraries inbok uses, so a change of emulate version shows up here
// first.

// startTestEmulator starts an emulator with one verified account, one
// unverified account, and inbok's client. emulate adds its own
// testuser@gmail.com too.
func startTestEmulator(t *testing.T) (*Emulator, *oauth2.Config) {
	t.Helper()
	e := Start(t, Seed{
		Users: []User{
			{Email: "alex@example.com", Name: "Alex Rivera"},
			{Email: "unverified@example.com", Name: "Unverified", Unverified: true},
		},
		Clients: []Client{{ID: testClientID, Secret: testSecret, RedirectURIs: []string{testRedirect}}},
		Messages: []Message{
			{ID: "m1", Mailbox: "alex@example.com", From: "blair@example.com", Subject: "Hello", Body: "Hi."},
		},
	})
	cfg := &oauth2.Config{
		ClientID:     testClientID,
		ClientSecret: testSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:   e.BaseURL.JoinPath("/o/oauth2/v2/auth").String(),
			TokenURL:  e.BaseURL.JoinPath("/oauth2/token").String(),
			AuthStyle: oauth2.AuthStyleInParams,
		},
		RedirectURL: testRedirect,
		Scopes:      strings.Fields(testScopes),
	}
	return e, cfg
}

// connect runs the authorization code flow with PKCE for email and returns
// the token response.
func connect(t *testing.T, e *Emulator, cfg *oauth2.Config, email string) *oauth2.Token {
	t.Helper()
	verifier := oauth2.GenerateVerifier()
	authURL := cfg.AuthCodeURL("state-1", oauth2.AccessTypeOffline, oauth2.ApprovalForce, oauth2.S256ChallengeOption(verifier))

	back := e.Consent(t, authURL, email)
	require.Equal(t, "state-1", back.Query().Get("state"))

	token, err := cfg.Exchange(t.Context(), back.Query().Get("code"), oauth2.VerifierOption(verifier))
	require.NoError(t, err)
	return token
}

// idTokenClaims decodes the ID token in token without checking its
// signature, as inbok does for a token straight from the token endpoint.
func idTokenClaims(t *testing.T, token *oauth2.Token) map[string]any {
	t.Helper()
	raw, ok := token.Extra("id_token").(string)
	require.True(t, ok, "no id_token")
	parts := strings.Split(raw, ".")
	require.Len(t, parts, 3)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(payload, &claims))
	return claims
}

func TestEmulatorConnect(t *testing.T) {
	e, cfg := startTestEmulator(t)

	token := connect(t, e, cfg, "alex@example.com")

	assert.NotEmpty(t, token.RefreshToken)
	assert.Equal(t, testScopes, token.Extra("scope"))
	claims := idTokenClaims(t, token)
	assert.Equal(t, e.BaseURL.String(), claims["iss"])
	assert.Equal(t, testClientID, claims["aud"])
	assert.NotEmpty(t, claims["sub"])
	assert.Equal(t, "alex@example.com", claims["email"])
	assert.Equal(t, true, claims["email_verified"])
}

func TestEmulatorUnverifiedEmail(t *testing.T) {
	e, cfg := startTestEmulator(t)

	token := connect(t, e, cfg, "unverified@example.com")

	assert.Equal(t, false, idTokenClaims(t, token)["email_verified"])
}

// requireRefused checks that err is the token endpoint refusing with code.
func requireRefused(t *testing.T, code string, err error) {
	t.Helper()
	var refused *oauth2.RetrieveError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, code, refused.ErrorCode)
}

func TestEmulatorWrongVerifier(t *testing.T) {
	e, cfg := startTestEmulator(t)
	authURL := cfg.AuthCodeURL("state-1", oauth2.S256ChallengeOption(oauth2.GenerateVerifier()))
	back := e.Consent(t, authURL, "alex@example.com")

	_, err := cfg.Exchange(t.Context(), back.Query().Get("code"), oauth2.VerifierOption(oauth2.GenerateVerifier()))

	requireRefused(t, "invalid_grant", err)
}

func TestEmulatorWrongSecret(t *testing.T) {
	e, cfg := startTestEmulator(t)
	back := e.Consent(t, cfg.AuthCodeURL("state-1"), "alex@example.com")
	cfg.ClientSecret = "GOCSPX-wrong"

	_, err := cfg.Exchange(t.Context(), back.Query().Get("code"))

	requireRefused(t, "invalid_client", err)
}

// Gmail calls check the access token's scopes; without this the emulator
// would answer any token (strict_scopes in the seed).
func TestEmulatorScopes(t *testing.T) {
	e, cfg := startTestEmulator(t)
	cfg.Scopes = []string{"openid", "email"}
	token := connect(t, e, cfg, "alex@example.com")
	service, err := gmail.NewService(t.Context(),
		option.WithTokenSource(cfg.TokenSource(t.Context(), token)),
		option.WithEndpoint(e.BaseURL.String()+"/"))
	require.NoError(t, err)

	_, err = service.Users.Messages.List("me").Context(t.Context()).Do()

	var refused *googleapi.Error
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, http.StatusForbidden, refused.Code)
}

func TestEmulatorRevoke(t *testing.T) {
	e, cfg := startTestEmulator(t)
	token := connect(t, e, cfg, "alex@example.com")
	client := &http.Client{Timeout: requestTimeout}

	// When the refresh token is revoked.
	resp, err := client.PostForm(e.BaseURL.JoinPath("/oauth2/revoke").String(), url.Values{"token": {token.RefreshToken}})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Then a new access token is refused. A token with only the refresh
	// token makes the source ask for one.
	_, err = cfg.TokenSource(t.Context(), &oauth2.Token{RefreshToken: token.RefreshToken}).Token()
	requireRefused(t, "invalid_grant", err)
}
