package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/attemptlimit"
	"inbok/internal/session"
	"inbok/internal/totp"
)

const retryAfterHeader = "Retry-After"

const signInThrottledType = problemPrefix + "sign-in-throttled"

// throttleProblem maps shared setup and sign-in throttling to safe text, with
// a Retry-After header when err carries a wait.
func throttleProblem(err error) error {
	if errors.Is(err, session.ErrThrottled) {
		return retryAfterProblem(newProblem(http.StatusTooManyRequests, signInThrottledType, "Too many attempts", "Too many failed attempts."), err)
	}

	return err
}

const totpThrottledType = problemPrefix + "totp-throttled"

// totpThrottleProblem maps code throttling to safe text, with a Retry-After
// header when err carries a wait.
func totpThrottleProblem(err error) error {
	if errors.Is(err, totp.ErrThrottled) {
		return retryAfterProblem(newProblem(http.StatusTooManyRequests, totpThrottledType, "Too many code attempts", "Too many failed code attempts."), err)
	}

	return err
}

// retryAfterProblem returns p with a Retry-After header holding the wait in
// err's [*attemptlimit.Error], from [retryAfterSeconds]. Without one it
// returns p unchanged.
func retryAfterProblem(p *problem, err error) error {
	refusal, ok := errors.AsType[*attemptlimit.Error](err)
	if !ok {
		return p
	}
	return huma.ErrorWithHeaders(p, http.Header{retryAfterHeader: {retryAfterSeconds(refusal.Wait)}})
}

// retryAfterSeconds returns wait as a Retry-After value: whole seconds,
// rounded up, at least 1.
func retryAfterSeconds(wait time.Duration) string {
	// Rounding down would invite a retry that is still refused.
	seconds := max(1, (wait+time.Second-1)/time.Second)
	return strconv.FormatInt(int64(seconds), 10)
}
