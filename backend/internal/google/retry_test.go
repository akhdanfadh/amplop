package google

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
)

// stubToken is the access token stubbed Gmail calls send.
var stubToken = connectedaccount.NewAccessToken("access-1")

// pauses records the waits a client made between retries, without waiting.
type pauses struct {
	mu    sync.Mutex
	waits []time.Duration
}

// record notes d and returns at once.
func (p *pauses) record(_ context.Context, d time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.waits = append(p.waits, d)
	return nil
}

// all returns the waits recorded so far.
func (p *pauses) all() []time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Duration(nil), p.waits...)
}

// stubGmail starts handler as Gmail on this machine and returns a client
// pointed at it, whose clock reads testNow and whose retries wait nothing
// and are recorded.
func stubGmail(t *testing.T, handler http.HandlerFunc) (*Client, *pauses) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base},
		func() time.Time { return testNow })
	p := &pauses{}
	client.pause = p.record
	return client, p
}

// refuse writes a Gmail error with status and reason.
func refuse(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":` + strconv.Itoa(status) + `,"message":"refused",` +
		`"errors":[{"domain":"usageLimits","reason":"` + reason + `","message":"refused"}]}}`))
}

// profileAfter answers getProfile with refusal for the first refusals
// calls, then with a history ID, and counts the calls.
func profileAfter(refusals int, refusal func(http.ResponseWriter)) (http.HandlerFunc, *atomic.Int32) {
	var calls atomic.Int32
	return func(w http.ResponseWriter, _ *http.Request) {
		if int(calls.Add(1)) <= refusals {
			refusal(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"historyId":"42"}`))
	}, &calls
}

// Each kind of refusal waiting can outlast is retried, with growing waits.
func TestRetryRefusals(t *testing.T) {
	tests := []struct {
		name   string
		refuse func(http.ResponseWriter)
	}{
		{name: "too many requests", refuse: func(w http.ResponseWriter) { refuse(w, http.StatusTooManyRequests, "rateLimitExceeded") }},
		{name: "rate limit 403", refuse: func(w http.ResponseWriter) { refuse(w, http.StatusForbidden, reasonRateLimit) }},
		{name: "user rate limit 403", refuse: func(w http.ResponseWriter) { refuse(w, http.StatusForbidden, reasonUserRateLimit) }},
		{name: "backend error", refuse: func(w http.ResponseWriter) { refuse(w, http.StatusInternalServerError, "backendError") }},
		{name: "unavailable", refuse: func(w http.ResponseWriter) { refuse(w, http.StatusServiceUnavailable, "backendError") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler, calls := profileAfter(2, tc.refuse)
			client, p := stubGmail(t, handler)

			position, err := client.Position(t.Context(), stubToken)

			require.NoError(t, err)
			assert.Equal(t, "42", position)
			assert.Equal(t, int32(3), calls.Load())
			waits := p.all()
			require.Len(t, waits, 2)
			assert.GreaterOrEqual(t, waits[0], backoffFirst)
			assert.Less(t, waits[0], backoffFirst+backoffJitter)
			assert.GreaterOrEqual(t, waits[1], 2*backoffFirst)
			assert.Less(t, waits[1], 2*backoffFirst+backoffJitter)
		})
	}
}

// A refusal waiting does not cure returns at once, as its status only.
func TestRetryNotRetried(t *testing.T) {
	for _, reason := range []string{"dailyLimitExceeded", "domainPolicy"} {
		t.Run(reason, func(t *testing.T) {
			handler, calls := profileAfter(1, func(w http.ResponseWriter) { refuse(w, http.StatusForbidden, reason) })
			client, p := stubGmail(t, handler)

			_, err := client.Position(t.Context(), stubToken)

			require.Error(t, err)
			require.NotErrorIs(t, err, mailsync.ErrProviderBusy)
			assert.Contains(t, err.Error(), "403")
			assert.NotContains(t, err.Error(), reason)
			assert.Equal(t, int32(1), calls.Load())
			assert.Empty(t, p.all())
		})
	}
}

// Past the attempts, the refusal is the provider being busy, and the waits
// stay within the backoff.
func TestRetryBusy(t *testing.T) {
	handler, calls := profileAfter(retryAttempts, func(w http.ResponseWriter) {
		refuse(w, http.StatusTooManyRequests, "rateLimitExceeded")
	})
	client, p := stubGmail(t, handler)

	_, err := client.Position(t.Context(), stubToken)

	require.ErrorIs(t, err, mailsync.ErrProviderBusy)
	assert.Contains(t, err.Error(), "429")
	assert.NotContains(t, err.Error(), "refused")
	assert.Equal(t, int32(retryAttempts), calls.Load())
	waits := p.all()
	require.Len(t, waits, retryAttempts-1)
	for i, wait := range waits {
		assert.GreaterOrEqual(t, wait, backoffFirst<<i)
		assert.Less(t, wait, min(backoffFirst<<i, backoffMax)+backoffJitter)
	}
}

// A Retry-After within the backoff is waited, in seconds or as a date; a
// longer one returns busy at once.
func TestRetryAfter(t *testing.T) {
	tests := []struct {
		name     string
		after    string
		wantWait time.Duration
		busy     bool
	}{
		{name: "seconds", after: "7", wantWait: 7 * time.Second},
		{name: "date", after: testNow.Add(3 * time.Second).Format(http.TimeFormat), wantWait: 3 * time.Second},
		{name: "past date", after: testNow.Add(-time.Hour).Format(http.TimeFormat), wantWait: 0},
		{name: "too long", after: "3600", busy: true},
		{name: "past a duration", after: "99999999999999999", busy: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler, calls := profileAfter(1, func(w http.ResponseWriter) {
				w.Header().Set("Retry-After", tc.after)
				refuse(w, http.StatusTooManyRequests, "rateLimitExceeded")
			})
			client, p := stubGmail(t, handler)

			_, err := client.Position(t.Context(), stubToken)

			if tc.busy {
				require.ErrorIs(t, err, mailsync.ErrProviderBusy)
				assert.Equal(t, int32(1), calls.Load())
				assert.Empty(t, p.all())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []time.Duration{tc.wantWait}, p.all())
		})
	}
}

// An ended context stops the retries.
func TestRetryCancelled(t *testing.T) {
	handler, calls := profileAfter(retryAttempts, func(w http.ResponseWriter) {
		refuse(w, http.StatusServiceUnavailable, "backendError")
	})
	client, _ := stubGmail(t, handler)
	ctx, cancel := context.WithCancel(t.Context())
	client.pause = func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}

	_, err := client.Position(ctx, stubToken)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(1), calls.Load())
}
