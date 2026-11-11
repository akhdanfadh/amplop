package google

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"slices"
	"strconv"
	"time"

	"google.golang.org/api/googleapi"

	"inbok/internal/mailsync"
)

// Gmail's backoff, as its usage limits page describes it: wait 1, 2, 4
// seconds and so on, each plus up to a second at random, up to a maximum
// of typically 32 or 64 seconds. Retries stop after [retryAttempts], so a
// refusal that lasts, e.g. a daily limit, returns to the caller, whose
// durable step can wait far longer than a request should.
const (
	backoffFirst  = time.Second
	backoffMax    = 32 * time.Second
	backoffJitter = time.Second
	// retryAttempts counts the first try; the waits before the last add
	// up to about 15 to 19 seconds.
	retryAttempts = 5
)

// pause waits for d, or returns ctx's error once ctx ends first.
func pause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retry returns call's result, calling it again after a wait while it
// fails for a reason waiting can outlast, at most [retryAttempts] times.
// Once those run out, or Gmail asks to wait past [backoffMax], it returns
// an error wrapping [mailsync.ErrProviderBusy] and the last one's status;
// any other error is returned as call returned it.
func retry[T any](ctx context.Context, c *Client, call func() (T, error)) (T, error) {
	var zero T
	for attempt := 1; ; attempt++ {
		result, err := call()
		if err == nil {
			return result, nil
		}
		if !retryable(ctx, err) {
			return zero, err
		}
		wait, ok := retryWait(err, attempt, c.now())
		if !ok || attempt == retryAttempts {
			return zero, fmt.Errorf("%w: %w", mailsync.ErrProviderBusy, redactGmail(err))
		}
		if err := c.pause(ctx, wait); err != nil {
			return zero, err
		}
	}
}

// retryable reports whether err, from a Gmail call made with ctx, is one a
// later retry can outlast: a rate limit, a server error (Gmail's "Resolve
// errors" guide), or a request that timed out while ctx had not ended.
func retryable(ctx context.Context, err error) bool {
	refused, ok := errors.AsType[*googleapi.Error](err)
	if !ok {
		netErr, isNet := errors.AsType[net.Error](err)
		return isNet && netErr.Timeout() && ctx.Err() == nil
	}
	switch refused.Code {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	case http.StatusForbidden:
		return slices.ContainsFunc(refused.Errors, func(item googleapi.ErrorItem) bool {
			return item.Reason == reasonRateLimit || item.Reason == reasonUserRateLimit
		})
	}
	return false
}

// retryWait returns how long to wait, at now, after attempt failed with
// err: the Retry-After Gmail sent, or else the backoff for attempt. It
// returns false when Gmail asks for longer than [backoffMax].
func retryWait(err error, attempt int, now time.Time) (time.Duration, bool) {
	if refused, ok := errors.AsType[*googleapi.Error](err); ok {
		if after, ok := retryAfter(refused.Header, now); ok {
			return after, after <= backoffMax
		}
	}
	wait := min(backoffFirst<<(attempt-1), backoffMax)
	return wait + rand.N(backoffJitter), true
}

// retryAfterHeader is the header in which a server asks for a wait (RFC
// 9110 section 10.2.3).
const retryAfterHeader = "Retry-After"

// retryAfter returns the wait a Retry-After header in h asks for at now,
// in seconds or as a date (RFC 9110 section 10.2.3), and whether there was
// a usable one.
func retryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	value := h.Get(retryAfterHeader)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		// A wait past what a Duration holds is as good as forever.
		return time.Duration(min(seconds, int64(math.MaxInt64/time.Second))) * time.Second, true
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	return max(at.Sub(now), 0), true
}
