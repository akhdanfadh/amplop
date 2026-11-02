package httpapi

import (
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"golang.org/x/time/rate"

	"inbok/internal/session"
)

const (
	// A flood guard, not a rule people meet: over any 15 minutes it allows
	// more requests (20 + 900) than any per-client limit in the database,
	// so only machine-speed bursts reach it first.
	requestBurst = 20
	requestRate  = rate.Limit(1) // tokens per second

	// sweepInterval is how often full buckets are deleted.
	sweepInterval = time.Minute
	// maxBuckets caps memory at about 20 MB, however many clients a flood
	// uses.
	maxBuckets = 100_000

	// ipv6ClientBits is the IPv6 prefix one client is counted by: one
	// connection usually owns a whole /64.
	ipv6ClientBits = 64

	tooManyRequestsType = problemPrefix + "too-many-requests"
	rateLimitCause      = "Too many requests from this client in a short time."
)

// rateLimitKey names one client's bucket for one operation.
type rateLimitKey struct {
	operation string       // the operation's ID
	client    netip.Prefix // from clientPrefix
}

// rateLimiter keeps a token bucket per operation and client, in memory.
type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[rateLimitKey]*rate.Limiter
	now       func() time.Time
	lastSweep time.Time
}

// newRateLimiter returns a rateLimiter that reads time from now.
func newRateLimiter(now func() time.Time) *rateLimiter {
	return &rateLimiter{buckets: map[rateLimitKey]*rate.Limiter{}, now: now, lastSweep: now()}
}

// take spends one of key's tokens and returns zero, or, when none is left,
// how long until one is.
func (l *rateLimiter) take(key rateLimitKey) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if now.Sub(l.lastSweep) >= sweepInterval {
		l.sweep(now)
	}
	bucket, ok := l.buckets[key]
	if !ok {
		// Full: make room now, or refuse new clients until a sweep can.
		if len(l.buckets) >= maxBuckets {
			l.sweep(now)
		}
		if len(l.buckets) >= maxBuckets {
			return time.Second
		}
		bucket = rate.NewLimiter(requestRate, requestBurst)
		l.buckets[key] = bucket
	}
	if bucket.AllowN(now, 1) {
		return 0
	}
	missing := 1 - bucket.TokensAt(now)
	return time.Duration(missing / float64(requestRate) * float64(time.Second))
}

// sweep keeps only the buckets that are not full at now. A full bucket acts
// like a missing one, so nothing is lost. A new map frees the memory, which
// deleting from a Go map does not.
func (l *rateLimiter) sweep(now time.Time) {
	l.lastSweep = now
	kept := make(map[rateLimitKey]*rate.Limiter, len(l.buckets)/2)
	for key, bucket := range l.buckets {
		if bucket.TokensAt(now) < requestBurst {
			kept[key] = bucket
		}
	}
	l.buckets = kept
}

// clientPrefix returns what addr is counted as: an IPv4 address alone, or
// its IPv6 /64, so one connection cannot rotate addresses within its block.
func clientPrefix(addr netip.Addr) netip.Prefix {
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		return netip.PrefixFrom(addr, addr.BitLen())
	}
	return netip.PrefixFrom(addr, ipv6ClientBits).Masked()
}

// rateLimited reports whether op is one withRateLimit limits: a write anyone
// can send without a session.
func rateLimited(op *huma.Operation) bool {
	level, ok := op.Metadata[accessKey].(session.Access)
	return ok && level == session.AccessPublic && op.Method != http.MethodGet
}

// withRateLimit answers 429 with Retry-After when a request's client has used
// up its budget for a [rateLimited] operation, and passes every other request
// on.
//
// NOTE: The database limits of sign-in and setup decide outcomes, but sit
// behind a lock or a password hash, so refusing a flood there still costs a
// connection per request. This refuses in memory first. It counts per
// process and forgets on restart, which suits a flood guard; the exact
// counts stay in the database.
func withRateLimit(limiter *rateLimiter) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		if !rateLimited(op) {
			next(ctx)
			return
		}
		var client netip.Addr
		// Only a non-TCP listener lacks an address. The request then passes
		// unlimited; operations that need the address refuse it themselves.
		if errs := resolveClientAddr(ctx, &client); errs != nil {
			next(ctx)
			return
		}

		wait := limiter.take(rateLimitKey{operation: op.OperationID, client: clientPrefix(client)})
		if wait == 0 {
			next(ctx)
			return
		}
		ginCtx := humagin.Unwrap(ctx)
		ginCtx.Header(retryAfterHeader, retryAfterSeconds(wait))
		writeProblem(ginCtx, tooManyRequestsProblem())
	}
}

// tooManyRequestsProblem is the response withRateLimit sends.
func tooManyRequestsProblem() *problem {
	return newProblem(http.StatusTooManyRequests, tooManyRequestsType, "Too many requests", "Too many requests in a short time.")
}

// documentRateLimits adds withRateLimit's 429 to every [rateLimited]
// operation's documented responses, beside any 429 the operation sends
// itself. Call it once, after every operation is registered, before
// registerProblemTypes.
func documentRateLimits(api huma.API) {
	status := strconv.Itoa(http.StatusTooManyRequests)
	for _, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Options, item.Head, item.Patch, item.Trace} {
			if op == nil || !rateLimited(op) {
				continue
			}

			throttled, ok := op.Responses[status]
			if !ok {
				throttled = problemResponses(api, http.StatusTooManyRequests)[status]
				throttled.Description = rateLimitCause
				op.Responses[status] = throttled
			} else {
				throttled.Description += " " + rateLimitCause
			}

			// One response holds both problems as named examples.
			media := throttled.Content[problemContentType]
			if media.Examples == nil {
				media.Examples = map[string]*huma.Example{}
			}
			if media.Example != nil {
				media.Examples["throttled"] = &huma.Example{Value: media.Example}
				media.Example = nil
			}
			media.Examples["tooManyRequests"] = &huma.Example{Value: tooManyRequestsProblem()}
		}
	}
}
