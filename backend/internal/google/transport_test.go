package google

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/connectedaccount"
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
// so the code, secret, and tokens never leave it.
func TestClientLoopbackOnly(t *testing.T) {
	base, err := url.Parse("http://[2001:db8::1]:4000")
	require.NoError(t, err)
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base}, time.Now)

	_, exchangeErr := client.Exchange(t.Context(), "code-1", "verifier-1")
	_, refreshErr := client.Refresh(t.Context(), "refresh-1")
	_, profileErr := client.Profile(t.Context(), connectedaccount.NewAccessToken("access-1"))

	require.ErrorIs(t, exchangeErr, errNotLoopback)
	require.ErrorIs(t, refreshErr, errNotLoopback)
	require.ErrorIs(t, profileErr, errNotLoopback)
}

// A request that outlasts the timeout ends with the context's deadline,
// which also reads as a timeout.
func TestTimeoutAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	client := httpClient(reachAny, 50*time.Millisecond)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	response, err := client.Do(request)
	if response != nil {
		_ = response.Body.Close()
	}

	require.ErrorIs(t, err, context.DeadlineExceeded)
	timeout, ok := errors.AsType[net.Error](err)
	require.True(t, ok)
	assert.True(t, timeout.Timeout())
}

// The timeout covers reading the answer, not only waiting for it.
func TestTimeoutBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	client := httpClient(reachAny, 50*time.Millisecond)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	response, err := client.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })

	_, err = io.ReadAll(response.Body)

	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// recordingTransport answers every request with ok and keeps the last
// request's context.
type recordingTransport struct {
	ctx context.Context
	err error
}

// RoundTrip records req's context and answers with ok, or with t.err.
func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.ctx = req.Context()
	if t.err != nil {
		return nil, t.err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

// A request's deadline lasts until its answer is closed, then is released.
func TestTimeoutReleased(t *testing.T) {
	base := &recordingTransport{}
	transport := timeoutTransport{base: base, timeout: time.Minute}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1/", nil)
	require.NoError(t, err)

	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	_, deadline := base.ctx.Deadline()

	require.True(t, deadline)
	require.NoError(t, base.ctx.Err())
	require.NoError(t, response.Body.Close())
	assert.ErrorIs(t, base.ctx.Err(), context.Canceled)
}

// A request that fails releases its deadline at once.
func TestTimeoutReleasedOnError(t *testing.T) {
	base := &recordingTransport{err: errors.New("refused")}
	transport := timeoutTransport{base: base, timeout: time.Minute}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1/", nil)
	require.NoError(t, err)

	_, err = transport.RoundTrip(request) //nolint:bodyclose // No answer comes with an error.

	require.Error(t, err)
	assert.ErrorIs(t, base.ctx.Err(), context.Canceled)
}

// No client sets [http.Client.Timeout]: with a transport net/http does not
// know, such as [oauth2.Transport], it cancels through the deprecated
// [http.Request.Cancel] and [oauth2.Transport.CancelRequest].
func TestNoClientTimeout(t *testing.T) {
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect}, time.Now)

	assert.Zero(t, client.http.Timeout)
	assert.Zero(t, client.authorized(connectedaccount.NewAccessToken("access-1"), maxResponseBytes).Timeout)
}
