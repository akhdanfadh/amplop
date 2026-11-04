package account

import (
	"errors"
	"unicode/utf8"
)

// Password bounds count Unicode code points, matching HTTP setup validation.
const (
	PasswordMinLength = 15
	PasswordMaxLength = 128
)

// ErrPasswordInvalid means a password breaks the length rule.
var ErrPasswordInvalid = errors.New("password must contain 15 to 128 characters")

// ValidatePassword returns [ErrPasswordInvalid] unless password is valid UTF-8
// of [PasswordMinLength] to [PasswordMaxLength] characters, and nil otherwise.
func ValidatePassword(password string) error {
	length := utf8.RuneCountInString(password)
	if !utf8.ValidString(password) || length < PasswordMinLength || length > PasswordMaxLength {
		return ErrPasswordInvalid
	}
	return nil
}
