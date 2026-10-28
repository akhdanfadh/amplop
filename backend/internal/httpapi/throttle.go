package httpapi

import (
	"errors"
	"net/http"

	"inbok/internal/session"
)

const signInThrottledType = problemPrefix + "sign-in-throttled"

// throttleProblem maps shared setup and sign-in throttling to safe text.
func throttleProblem(err error) error {
	if errors.Is(err, session.ErrThrottled) {
		return newProblem(http.StatusTooManyRequests, signInThrottledType, "Too many attempts", "Too many failed attempts. Try again later.")
	}

	return err
}
