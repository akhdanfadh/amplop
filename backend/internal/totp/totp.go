// Package totp owns authenticator enrollment data and time-based code rules.
package totp

import (
	"errors"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	otptotp "github.com/pquerna/otp/totp"
)

const (
	issuer            = "inbok"           // Identifies our service in the authenticator.
	period            = 30                // RFC 6238 section 5.2: balance security and time to enter a code.
	secretSize        = 20                // RFC 4226 section 4 recommends a 160-bit secret.
	encodedSecretSize = 32                // Base32 represents 160 bits as 32 characters of 5 bits each.
	codeDigits        = otp.DigitsSix     // RFC 4226 section 4 minimum; matches authenticator defaults.
	algorithm         = otp.AlgorithmSHA1 // HMAC-SHA1 matches the default authenticator URI format.
)

// ErrInvalidCode covers wrong, malformed, expired, and already accepted codes.
var ErrInvalidCode = errors.New("invalid TOTP code")

var (
	errSecret = errors.New("invalid stored TOTP secret")
	errClock  = errors.New("TOTP clock is before the Unix epoch")
)

// Verifier checks codes using a supplied clock, without retaining user state.
type Verifier struct {
	now func() time.Time
}

// NewVerifier uses now for the code window. Production supplies time.Now.
func NewVerifier(now func() time.Time) *Verifier {
	return &Verifier{now: now}
}

// Check returns the matched 30-second step, accepting one step of clock skew.
// Pass -1 when no code has been accepted. Storage must lock the factor, pass its
// last accepted step, and save the returned step in the same transaction.
func (v *Verifier) Check(secret, code string, lastStep int64) (int64, error) {
	if len(code) != codeDigits.Length() {
		return 0, ErrInvalidCode
	}
	for _, digit := range []byte(code) {
		if digit < '0' || digit > '9' {
			return 0, ErrInvalidCode
		}
	}
	if len(secret) != encodedSecretSize {
		return 0, errSecret
	}
	seconds := v.now().Unix()
	if seconds < 0 {
		return 0, errClock
	}
	current := seconds / period

	// Check newest first so a rare code collision consumes the newest match.
	for step := current + 1; step >= current-1 && step >= 0; step-- {
		matched, err := hotp.ValidateCustom(code, uint64(step), secret, hotp.ValidateOpts{
			Digits:    codeDigits,
			Algorithm: algorithm,
		})
		if err != nil {
			return 0, errSecret
		}
		if !matched {
			continue
		}
		if step <= lastStep {
			return 0, ErrInvalidCode
		}
		return step, nil
	}
	return 0, ErrInvalidCode
}

type enrollment struct {
	secret string
	uri    string
}

// newEnrollment fixes authenticator settings and generates a fresh random secret.
// Only the enrollment operation may reveal the secret and URI to the user.
func newEnrollment(email string) (enrollment, error) {
	key, err := otptotp.Generate(otptotp.GenerateOpts{
		Issuer:      issuer,
		AccountName: email,
		Period:      period,
		SecretSize:  secretSize,
		Digits:      codeDigits,
		Algorithm:   algorithm,
	})
	if err != nil {
		return enrollment{}, err
	}
	return enrollment{secret: key.Secret(), uri: key.URL()}, nil
}
