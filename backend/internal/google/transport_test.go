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

// A redirect from the token endpoint is not followed, so the code and
// secret go nowhere else.
func TestExchangeNoRedirect(t *testing.T) {
	followed := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	t.Cleanup(elsewhere.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/oauth2/token", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base}, time.Now)

	_, err = client.Exchange(t.Context(), "code-1", "verifier-1")

	require.Error(t, err)
	assert.False(t, followed)
}

func TestDialLoopback(t *testing.T) {
	cases := []struct {
		address string
		allowed bool
	}{
		{"127.0.0.1:4000", true},
		{"127.8.9.10:4000", true},
		{"[::1]:4000", true},
		{"[::ffff:127.0.0.1]:4000", true},
		{"192.0.2.10:4000", false},
		{"[2001:db8::1]:4000", false},
		{"localhost:4000", false},
	}
	for _, tc := range cases {
		t.Run(tc.address, func(t *testing.T) {
			err := dialLoopback("tcp", tc.address, nil)

			if tc.allowed {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, errNotLoopback)
			}
		})
	}
}

// With a base URL, the client itself refuses to connect off this machine,
// so the code, secret, and token never leave it.
func TestClientLoopbackOnly(t *testing.T) {
	base, err := url.Parse("http://[2001:db8::1]:4000")
	require.NoError(t, err)
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base}, time.Now)

	_, exchangeErr := client.Exchange(t.Context(), "code-1", "verifier-1")
	_, profileErr := client.Profile(t.Context(), connectedaccount.NewAccessToken("access-1"))

	require.ErrorIs(t, exchangeErr, errNotLoopback)
	require.ErrorIs(t, profileErr, errNotLoopback)
}
