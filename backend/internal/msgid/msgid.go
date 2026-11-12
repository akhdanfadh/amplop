// Package msgid holds inbok's rules for the msg-ids that thread mail: the
// Message-ID, In-Reply-To, and References headers (RFC 5322 section 3.6.4).
// Sync trims References with it; whatever msg-id inbok writes, e.g. its own
// Message-IDs, passes [Writable] first. Sync's parser stays more lenient,
// since matching received mail is another job.
package msgid

import "strings"

// MaxReferences bounds a References list, inbok's choice. Netnews trims
// References the same way, keeping the first msg-id and the last two
// (RFC 5537 section 3.4.4).
const MaxReferences = 50

// maxLength bounds a msg-id in octets, with its angle brackets: RFC 5536
// section 3.1.3 limits a Netnews Message-ID to 250.
//
// DESIGN: within it, a msg-id always fits one header line. go-message
// v0.18.2 breaks a word longer than a line's 998 octets with a fold inside
// it, which turns one msg-id into two.
const maxLength = 250

// Writable reports whether id is a msg-id inbok may write: RFC 5322's
// current syntax, "<", dot-atom-text, "@", dot-atom-text or a domain literal
// of dtext, ">" (section 3.6.4), at most 250 octets in all. A received ID
// without "@" is invalid in every syntax; one with a quoted local part is
// the obsolete syntax, which readers still accept but section 4 forbids
// generating, so inbok leaves both out. Each writable ID is one header
// value, as it holds no space, control character, or bracket outside the
// literal.
func Writable(id string) bool {
	if len(id) > maxLength {
		return false
	}
	inner, ok := strings.CutPrefix(id, "<")
	if !ok {
		return false
	}
	inner, ok = strings.CutSuffix(inner, ">")
	if !ok {
		return false
	}
	left, right, ok := strings.Cut(inner, "@")
	return ok && DotAtom(left) && (DotAtom(right) || domainLiteral(right))
}

// DotAtom reports whether s is ASCII dot-atom text: atext characters in
// dot-separated parts that are not empty (RFC 5322 section 3.2.3).
func DotAtom(s string) bool {
	for part := range strings.SplitSeq(s, ".") {
		if part == "" || strings.ContainsFunc(part, func(r rune) bool { return !atext(r) }) {
			return false
		}
	}
	return true
}

// atext reports whether r is an RFC 5322 atext character (section 3.2.3).
func atext(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9':
		return true
	}
	return strings.ContainsRune("!#$%&'*+-/=?^_`{|}~", r)
}

// domainLiteral reports whether s is a msg-id's no-fold-literal: "[", dtext,
// "]", where dtext is printable ASCII other than "[", "]", and "\"
// (RFC 5322 sections 3.4.1 and 3.6.4).
func domainLiteral(s string) bool {
	inner, ok := strings.CutPrefix(s, "[")
	if !ok {
		return false
	}
	inner, ok = strings.CutSuffix(inner, "]")
	return ok && !strings.ContainsFunc(inner, func(r rune) bool {
		return r <= ' ' || r >= 0x7f || r == '[' || r == ']' || r == '\\'
	})
}

// TrimReferences returns ids, a References list, at most [MaxReferences]
// long: the first, the thread's start, and the last ones, the nearest
// parents, since a reply's References is its parent's followed by the
// parent's Message-ID (RFC 5322 section 3.6.4).
func TrimReferences(ids []string) []string {
	if len(ids) <= MaxReferences {
		return ids
	}
	return append([]string{ids[0]}, ids[len(ids)-MaxReferences+1:]...)
}
