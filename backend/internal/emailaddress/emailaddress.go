// Package emailaddress checks and normalizes email addresses. [Parse] takes
// inbok's own: account emails, addresses compared or sent to on their behalf,
// the system email settings, and connected Gmail accounts. [ParseRecipient]
// takes the addresses a member sends mail to.
//
// DESIGN: For its own addresses inbok lowercases the ASCII letters of a whole
// address, local part included, and compares addresses exactly. RFC 5321
// lets a mail server treat the local part's case as meaningful; an address
// on such a server cannot be an inbok account's email or the system sender.
// Non-ASCII letters keep their case, because PostgreSQL's lower() follows the
// database's locale and could disagree with Go.
package emailaddress

import (
	"errors"
	"net/mail"
	"strings"
	"unicode"
)

// maxLength is the most octets an address may have in UTF-8: RFC 5321's
// 256-octet path less its angle brackets, which RFC 6531 keeps for UTF-8
// addresses. Huma and the users table count 254 characters, which this never
// exceeds.
const maxLength = 254

// ErrInvalid means the input is not one bare email address of at most 254
// octets without hidden characters. It holds no part of the input.
var ErrInvalid = errors.New("email address is not valid")

// Parse returns address without surrounding spaces or tabs and with its ASCII
// letters lowercased. It returns [ErrInvalid] unless address is a single
// address with no display name, angle brackets, or quoted local part, the
// rule Huma applies to format "email", of at most 254 octets, that also has
// no hidden characters: controls, invisible formatting such as a zero-width
// space or a right-to-left override, other characters Unicode says to draw
// as nothing, such as a variation selector, and Unicode spaces and line or
// paragraph separators. Parse(Parse(s)) equals Parse(s).
func Parse(address string) (string, error) {
	parsed, err := parse(address)
	if err != nil {
		return "", err
	}
	return lowerASCII(parsed), nil
}

// ParseRecipient returns address without surrounding spaces or tabs, its
// letters' case kept. It returns [ErrInvalid] for what [Parse] refuses.
func ParseRecipient(address string) (string, error) {
	// A sender must keep the local part's case, which the recipient's server
	// may treat as meaningful (RFC 5321, section 2.4); inbok accepts that
	// cost only for its own addresses.
	return parse(address)
}

// parse returns address without surrounding spaces or tabs, or [ErrInvalid]
// unless it is one bare address without hidden characters, as [Parse]
// describes.
func parse(address string) (string, error) {
	// net/mail skips only spaces and tabs around an address, as Huma's check
	// does, so other white space is refused rather than trimmed.
	parsed, err := mail.ParseAddress(address)
	// net/mail also accepts a display name and unquotes a quoted local part,
	// so any difference from the trimmed input means it was not a bare
	// address.
	if err != nil || parsed.Name != "" || parsed.Address != strings.TrimSpace(address) {
		return "", ErrInvalid
	}
	if len(parsed.Address) > maxLength || strings.ContainsFunc(parsed.Address, hidden) {
		return "", ErrInvalid
	}
	return parsed.Address, nil
}

// hidden reports whether r is a control character, an invisible format
// character (Unicode category Cf), another default ignorable character
// (Other_Default_Ignorable_Code_Point, Variation_Selector), or a space or
// separator (Zs, Zl, Zp).
//
// NOTE: net/mail accepts these inside an address, so without this check
// "admin\u200b@example.com" would be saved as an account that looks the same
// as "admin@example.com". The SMTP adapter refuses only line breaks, NUL, and
// angle brackets.
func hidden(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Other_Default_Ignorable_Code_Point,
		unicode.Variation_Selector, unicode.Zs, unicode.Zl, unicode.Zp)
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
