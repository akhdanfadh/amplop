// Package emailaddress checks and normalizes inbok's own email addresses:
// account emails, addresses compared or sent to on their behalf, and the
// system email settings. Connected mail accounts are not covered.
//
// NOTE: inbok lowercases the ASCII letters of a whole address, local part
// included, and compares addresses exactly. RFC 5321 lets a mail server treat
// the local part's case as meaningful; an address on such a server cannot
// be an inbok account's email or the system sender.
// Non-ASCII letters keep their case, because PostgreSQL's lower() follows the
// database's locale and could disagree with Go. ADR025 records the choice.
package emailaddress

import (
	"errors"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxLength is the most characters an address may have: RFC 5321's 256-octet
// path less its angle brackets, counted in characters as Huma and the users
// table count it.
const maxLength = 254

// ErrInvalid means the input is not one bare email address of at most 254
// characters without hidden characters. It holds no part of the input.
var ErrInvalid = errors.New("email address is not valid")

// Parse returns address without surrounding spaces or tabs and with its ASCII
// letters lowercased. It returns [ErrInvalid] unless address is a single
// address with no display name, angle brackets, or quoted local part, the
// rule Huma applies to format "email", that also has no hidden characters:
// controls, invisible formatting such as a zero-width space or a
// right-to-left override, and Unicode spaces and line or paragraph
// separators. Parse(Parse(s)) equals Parse(s).
func Parse(address string) (string, error) {
	// net/mail skips only spaces and tabs around an address, as Huma's check
	// does, so other white space is refused rather than trimmed.
	parsed, err := mail.ParseAddress(address)
	// net/mail also accepts a display name and unquotes a quoted local part,
	// so any difference from the trimmed input means it was not a bare
	// address.
	if err != nil || parsed.Name != "" || parsed.Address != strings.TrimSpace(address) {
		return "", ErrInvalid
	}
	if utf8.RuneCountInString(parsed.Address) > maxLength || strings.ContainsFunc(parsed.Address, hidden) {
		return "", ErrInvalid
	}
	return lowerASCII(parsed.Address), nil
}

// hidden reports whether r is a control character, an invisible format
// character (Unicode category Cf), or a space or separator (Zs, Zl, Zp).
//
// NOTE: net/mail accepts these inside an address, so without this check
// "admin\u200b@example.com" would be saved as an account that looks the same
// as "admin@example.com". The SMTP adapter refuses only line breaks, NUL, and
// angle brackets.
func hidden(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zs, unicode.Zl, unicode.Zp)
}

// lowerASCII returns s with A to Z replaced by a to z and every other byte
// unchanged, as PostgreSQL's lower(s COLLATE "C") does.
func lowerASCII(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}
