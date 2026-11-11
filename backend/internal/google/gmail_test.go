package google

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
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

// A call whose context ends reports the context's error, which callers
// compare with errors.Is, rather than an unreadable answer.
func TestProfileContextEnded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base}, time.Now)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	t.Cleanup(cancel)

	_, err = client.Profile(ctx, connectedaccount.NewAccessToken("access-1"))

	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// A 401 from Gmail reports the access token refused; other answers do
// not.
func TestRefusalTransport(t *testing.T) {
	for _, c := range []struct {
		status  int
		refused int
	}{{http.StatusUnauthorized, 1}, {http.StatusForbidden, 0}, {http.StatusOK, 0}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
		}))
		t.Cleanup(server.Close)
		refused := 0
		client := &http.Client{Transport: refusalTransport{base: http.DefaultTransport, refused: func() { refused++ }}}

		res, err := client.Get(server.URL)

		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		assert.Equal(t, c.refused, refused, "status %d", c.status)
	}
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

// An answer past the cap fails the call rather than being read whole.
func TestGmailTooLarge(t *testing.T) {
	client, _ := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"historyId":"42","emailAddress":"` + strings.Repeat("a", maxResponseBytes) + `"}`))
	})

	_, err := client.Position(t.Context(), stubToken)

	require.ErrorIs(t, err, errResponseTooLarge)
}

// An answer that does not decode fails the call without quoting it.
func TestGmailMalformed(t *testing.T) {
	client, _ := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"historyId":"private-marker"}`))
	})

	_, err := client.Position(t.Context(), stubToken)

	require.ErrorIs(t, err, errGmailAnswer)
	assert.NotContains(t, err.Error(), "private-marker")
}

// The Google client's debug logging, which would log whole answers, stays
// off whatever the environment says.
func TestGmailNoDebugLog(t *testing.T) {
	t.Setenv("GOOGLE_SDK_GO_LOGGING_LEVEL", "debug")
	read, write, err := os.Pipe()
	require.NoError(t, err)
	stderr := os.Stderr
	os.Stderr = write
	t.Cleanup(func() { os.Stderr = stderr })
	client, _ := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"historyId":"42","emailAddress":"private-marker"}`))
	})

	_, err = client.Position(t.Context(), stubToken)
	require.NoError(t, err)
	os.Stderr = stderr
	require.NoError(t, write.Close())
	logged, err := io.ReadAll(read)
	require.NoError(t, err)

	assert.NotContains(t, string(logged), "private-marker")
}
