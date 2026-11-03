package httpapi

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/clientprefix"
)

// testClock is a clock tests move by hand.
type testClock struct{ now time.Time }

func (c *testClock) read() time.Time { return c.now }

func newTestLimiter() (*rateLimiter, *testClock) {
	clock := &testClock{now: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	return newRateLimiter(clock.read), clock
}

var (
	limitedClient = netip.MustParseAddr("192.0.2.1")
	otherClient   = netip.MustParseAddr("192.0.2.2")
)

// keyFor returns the key of client's bucket for operation.
func keyFor(operation string, client netip.Addr) rateLimitKey {
	return rateLimitKey{operation: operation, client: clientprefix.Of(client)}
}

func TestRateLimiterBudget(t *testing.T) {
	limiter, clock := newTestLimiter()
	key := keyFor("createSession", limitedClient)

	// A burst passes, then one more waits for the next token.
	for i := range requestBurst {
		require.Zero(t, limiter.take(key), "request %d", i+1)
	}
	assert.Equal(t, time.Second, limiter.take(key))

	// Another operation or client has its own bucket.
	assert.Zero(t, limiter.take(keyFor("createFirstAdmin", limitedClient)))
	assert.Zero(t, limiter.take(keyFor("createSession", otherClient)))

	// A token comes back each second, and a refusal spends none.
	clock.now = clock.now.Add(500 * time.Millisecond)
	assert.Equal(t, 500*time.Millisecond, limiter.take(key))
	clock.now = clock.now.Add(500 * time.Millisecond)
	assert.Zero(t, limiter.take(key))
	assert.Equal(t, time.Second, limiter.take(key))
}

// A sweep keeps only buckets that are not full, and runs once per interval.
func TestRateLimiterSweep(t *testing.T) {
	limiter, clock := newTestLimiter()
	start := clock.now
	idle := keyFor("createSession", limitedClient)
	busy := keyFor("createSession", otherClient)
	fresh := keyFor("createFirstAdmin", limitedClient)

	limiter.take(idle)
	clock.now = start.Add(sweepInterval - time.Second)
	for range requestBurst {
		limiter.take(busy)
	}
	// Before the interval, idle stays, though it is full again.
	limiter.take(busy)
	assert.Len(t, limiter.buckets, 2)

	clock.now = start.Add(sweepInterval)
	limiter.take(fresh)
	assert.ElementsMatch(t, []rateLimitKey{busy, fresh}, slices.Collect(maps.Keys(limiter.buckets)))

	// The next sweep waits a whole interval again.
	clock.now = start.Add(2*sweepInterval - time.Second)
	limiter.take(busy)
	assert.Contains(t, limiter.buckets, fresh)
	clock.now = start.Add(2 * sweepInterval)
	limiter.take(busy)
	assert.NotContains(t, limiter.buckets, fresh)
}

// At the cap, a new client waits until a sweep can make room.
func TestRateLimiterCap(t *testing.T) {
	limiter, clock := newTestLimiter()
	for i := range maxBuckets {
		client := netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)})
		limiter.take(keyFor("createSession", client))
	}
	newcomer := keyFor("createSession", limitedClient)

	assert.Equal(t, time.Second, limiter.take(newcomer))
	assert.Len(t, limiter.buckets, maxBuckets)

	// A second later every bucket is full again, so the sweep empties them.
	clock.now = clock.now.Add(time.Second)
	assert.Zero(t, limiter.take(newcomer))
	assert.Len(t, limiter.buckets, 1)
}

// The rule picks exactly the writes anyone can send without a session.
func TestRateLimitedOperations(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()

	var limited []string
	for _, item := range doc.Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Patch} {
			if op == nil {
				continue
			}
			response := op.Responses[strconv.Itoa(http.StatusTooManyRequests)]
			if !rateLimited(op) {
				// Only limited operations document the limiter's 429.
				if response != nil {
					assert.NotContains(t, response.Content[problemContentType].Examples, "tooManyRequests", op.OperationID)
				}
				continue
			}
			limited = append(limited, op.OperationID)

			// Each documents the 429, beside any of its own.
			require.NotNil(t, response, op.OperationID)
			assert.Contains(t, response.Description, rateLimitCause, op.OperationID)
			examples := response.Content[problemContentType].Examples
			assert.Equal(t, tooManyRequestsProblem(), examples["tooManyRequests"].Value, op.OperationID)
		}
	}
	assert.ElementsMatch(t, []string{
		"createProbeEvent", "createFirstAdmin", "createSession", "createSessionWithCode", "redeemEmailVerification",
		"requestRegistrationLink", "redeemRegistrationLink", "requestPasswordResetLink", "redeemPasswordResetLink",
	}, limited)

	// Sign-in keeps its own throttle example.
	signIn := doc.Paths[sessionsPath].Post.Responses[strconv.Itoa(http.StatusTooManyRequests)]
	assert.Contains(t, signIn.Content[problemContentType].Examples, "throttled")
	assert.Contains(t, doc.Components.Schemas.Map()[problemTypeSchemaName].Enum, tooManyRequestsType)
}

// testAPI returns an API whose rate limit reads a clock the test moves.
func testAPI() (*API, *testClock) {
	api := New(discard, Services{})
	clock := &testClock{now: time.Now()}
	api.limiter.now = clock.read
	return api, clock
}

// sendEmpty posts an empty object to path from peer, with forwardedFor in
// X-Forwarded-For unless it is empty. Validation answers 422 unless the rate
// limit answers first.
func sendEmpty(api *API, path, peer, forwardedFor string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, apiPrefix+path, strings.NewReader(`{}`))
	request.RemoteAddr = peer + ":1234"
	if forwardedFor != "" {
		request.Header.Set("X-Forwarded-For", forwardedFor)
	}
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func TestWithRateLimit(t *testing.T) {
	api, clock := testAPI()
	client := limitedClient.String()

	for i := range requestBurst {
		require.Equal(t, http.StatusUnprocessableEntity, sendEmpty(api, probeEventsPath, client, "").Code, "request %d", i+1)
	}
	refused := sendEmpty(api, probeEventsPath, client, "")

	require.Equal(t, http.StatusTooManyRequests, refused.Code)
	assert.Equal(t, "1", refused.Header().Get(retryAfterHeader))
	assert.Equal(t, problemContentType, refused.Header().Get("Content-Type"))
	body := decodeJSON(t, refused.Body.Bytes())
	assert.Equal(t, tooManyRequestsType, body["type"])
	assert.Equal(t, "Too many requests in a short time.", body["detail"])

	// Another client, or another operation, is unaffected.
	assert.Equal(t, http.StatusUnprocessableEntity, sendEmpty(api, probeEventsPath, otherClient.String(), "").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, sendEmpty(api, setupPath, client, "").Code)

	// A second later, one more request fits.
	clock.now = clock.now.Add(time.Second)
	assert.Equal(t, http.StatusUnprocessableEntity, sendEmpty(api, probeEventsPath, client, "").Code)
}

// The client is the address Gin trusts: the forwarded one from a trusted
// proxy, the connection's own otherwise.
func TestRateLimitBehindProxy(t *testing.T) {
	const proxy, stranger = "192.0.2.100", "192.0.2.50"
	api, _ := testAPI()
	require.NoError(t, api.SetTrustedProxies([]string{proxy + "/32"}))

	// Each client behind the proxy has its own budget.
	for i := range requestBurst + 1 {
		forwarded := "198.51.100." + strconv.Itoa(i+1)
		assert.Equal(t, http.StatusUnprocessableEntity, sendEmpty(api, probeEventsPath, proxy, forwarded).Code, forwarded)
	}

	// Nor by rotating addresses within its IPv6 /64.
	for i := range requestBurst {
		forwarded := "2001:db8:1:2::" + strconv.Itoa(i+1)
		require.Equal(t, http.StatusUnprocessableEntity, sendEmpty(api, probeEventsPath, proxy, forwarded).Code, forwarded)
	}
	assert.Equal(t, http.StatusTooManyRequests, sendEmpty(api, probeEventsPath, proxy, "2001:db8:1:2::99").Code)

	// A peer that is not trusted cannot escape by forging the header.
	for i := range requestBurst {
		forwarded := "203.0.113." + strconv.Itoa(i+1)
		require.Equal(t, http.StatusUnprocessableEntity, sendEmpty(api, probeEventsPath, stranger, forwarded).Code, forwarded)
	}
	assert.Equal(t, http.StatusTooManyRequests, sendEmpty(api, probeEventsPath, stranger, "203.0.113.99").Code)
}
