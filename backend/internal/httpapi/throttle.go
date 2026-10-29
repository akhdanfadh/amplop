package httpapi

import (
	"errors"
	"net/http"

	"inbok/internal/session"
	"inbok/internal/totp"
)

const signInThrottledType = problemPrefix + "sign-in-throttled"

// throttleProblem maps shared setup and sign-in throttling to safe text.
func throttleProblem(err error) error {
	if errors.Is(err, session.ErrThrottled) {
		return newProblem(http.StatusTooManyRequests, signInThrottledType, "Too many attempts", "Too many failed attempts. Try again later.")
	}

	return err
}

const totpThrottledType = problemPrefix + "totp-throttled"

func totpThrottleProblem(err error) error {
	if errors.Is(err, totp.ErrThrottled) {
		return newProblem(http.StatusTooManyRequests, totpThrottledType, "Too many code attempts", "Too many failed code attempts. Try again later.")
	}

	return err
}
