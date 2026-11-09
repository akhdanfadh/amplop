package google

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExchangeIDTokenChecks(t *testing.T) {
	now := testNow
	cases := []struct {
		name    string
		changes func(issuer string) map[string]any
		want    error
	}{
		{"Google's issuer", func(string) map[string]any { return map[string]any{"iss": "https://accounts.google.com"} }, errIDTokenIssuer},
		{"another audience", func(string) map[string]any { return map[string]any{"aud": "other.apps.googleusercontent.com"} }, errIDTokenAudience},
		{"several audiences", func(string) map[string]any { return map[string]any{"aud": []string{testClientID, "other"}} }, errIDTokenAudience},
		{"expired", func(string) map[string]any { return map[string]any{"exp": now.Unix()} }, errIDTokenExpired},
		{"no expiry", func(string) map[string]any { return map[string]any{"exp": nil} }, errIDTokenExpired},
		{"issuer with a username", func(issuer string) map[string]any {
			return map[string]any{"iss": strings.Replace(issuer, "://", "://x@", 1)}
		}, errIDTokenIssuer},
		{"no subject", func(string) map[string]any { return map[string]any{"sub": nil} }, errIDTokenIdentity},
		{"no email", func(string) map[string]any { return map[string]any{"email": nil} }, errIDTokenIdentity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := stubGoogle(t, now, func(issuer string) map[string]any {
				return tokenResponse(t, alexClaims(issuer, now, tc.changes(issuer)))
			})

			_, err := client.Exchange(t.Context(), "code-1", "verifier-1")

			require.ErrorIs(t, err, tc.want)
		})
	}
}

// Google has sent email_verified as the text "true"; the issuer is compared
// as config spells the base URL.
func TestExchangeSpellings(t *testing.T) {
	now := testNow
	client := stubGoogle(t, now, func(issuer string) map[string]any {
		return tokenResponse(t, alexClaims(issuer, now, map[string]any{
			"email_verified": "true",
			"iss":            strings.ToUpper(issuer) + "/",
		}))
	})

	grant, err := client.Exchange(t.Context(), "code-1", "verifier-1")
	require.NoError(t, err)

	assert.True(t, grant.EmailVerified)
}

// An aud of one entry in a list is the client's alone, so it passes.
func TestExchangeAudienceList(t *testing.T) {
	client := stubGoogle(t, testNow, func(issuer string) map[string]any {
		return tokenResponse(t, alexClaims(issuer, testNow, map[string]any{"aud": []string{testClientID}}))
	})

	_, err := client.Exchange(t.Context(), "code-1", "verifier-1")

	require.NoError(t, err)
}

// Google's own issuers are compared exactly, in both forms Google sends.
func TestParseIDTokenGoogleIssuer(t *testing.T) {
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect}, func() time.Time { return testNow })
	cases := []struct {
		issuer string
		want   error
	}{
		{"https://accounts.google.com", nil},
		{"accounts.google.com", nil},
		{"https://ACCOUNTS.google.com/", errIDTokenIssuer},
		{"https://accounts.example.com", errIDTokenIssuer},
	}
	for _, tc := range cases {
		t.Run(tc.issuer, func(t *testing.T) {
			token := idToken(t, alexClaims(tc.issuer, testNow, nil))

			_, err := client.parseIDToken(token)

			if tc.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.want)
			}
		})
	}
}

func TestParseIDTokenMalformed(t *testing.T) {
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect}, func() time.Time { return testNow })
	cases := map[string]any{
		"not text":        42,
		"two parts":       "a.b",
		"bad base64":      "a.!!!.c",
		"payload no JSON": "a." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".c",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := client.parseIDToken(value)

			require.Error(t, err)
		})
	}
}
