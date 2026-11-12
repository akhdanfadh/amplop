package google

import (
	"mime"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/emersion/go-message"
	// Registers the charsets RFC 2047 encoded words name beyond UTF-8 and
	// ISO-8859-1, e.g. Windows-1252 or Shift_JIS, for message.CharsetReader.
	_ "github.com/emersion/go-message/charset"
	gomail "github.com/emersion/go-message/mail"
	"google.golang.org/api/gmail/v1"

	"inbok/internal/mailsync"
	"inbok/internal/msgid"
)

// Limits on header text, which the sender writes. A header longer than
// [maxHeaderBytes] is cut before it is decoded or parsed, so a hostile one
// costs no more than any other; the counts bound what is kept of it.
const (
	// maxHeaderBytes is inbok's choice: 16 times RFC 5322's 998-octet line,
	// past any real subject or recipient list.
	maxHeaderBytes = 16 << 10
	// maxAddresses per list is inbok's choice: Gmail's own limit on the
	// recipients of a message it sends (Gmail's usage limits).
	maxAddresses = 500
)

// The headers a message's metadata is read from, spelled as the emulator
// sends them: messages.get's metadataHeaders matches names there only as
// written.
const (
	headerFrom       = "From"
	headerReplyTo    = "Reply-To"
	headerTo         = "To"
	headerCc         = "Cc"
	headerSubject    = "Subject"
	headerMessageID  = "Message-ID"
	headerInReplyTo  = "In-Reply-To"
	headerReferences = "References"
)

// headers is a message's header fields by name, as Gmail sends them.
type headers []*gmail.MessagePartHeader

// get returns the value of the first field named name, cut to
// [maxHeaderBytes], or "" when there is none.
func (h headers) get(name string) string {
	return cut(h.full(name))
}

// full returns the value of the first field named name, whole, or "" when
// there is none. Field names match in any case, as RFC 5322 spells them as
// ABNF strings (RFC 5234 section 2.3), and section 3.6 allows each of
// these headers once.
func (h headers) full(name string) string {
	for _, field := range h {
		// A second field of a name allowed once is malformed; the first is
		// inbok's choice, as good as any.
		if field != nil && strings.EqualFold(field.Name, name) {
			return field.Value
		}
	}
	return ""
}

// cut returns value without its bytes past [maxHeaderBytes], ending on a
// whole character, as a copy of its own.
func cut(value string) string {
	if len(value) <= maxHeaderBytes {
		return value
	}
	end := maxHeaderBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	// A slice would keep the whole value, perhaps megabytes, in memory.
	return strings.Clone(value[:end])
}

// cleanText returns text as PostgreSQL can store it: invalid UTF-8 and NUL,
// which its text and jsonb types refuse, each become U+FFFD. A sender
// controls both, e.g. with =?UTF-8?B?AA==?=, and one refused message would
// stop the account's every sync.
func cleanText(text string) string {
	text = strings.ToValidUTF8(text, string(utf8.RuneError))
	return strings.ReplaceAll(text, "\x00", string(utf8.RuneError))
}

// wordDecoder decodes RFC 2047 encoded words in any charset go-message knows.
var wordDecoder = &mime.WordDecoder{CharsetReader: message.CharsetReader}

// decodeText returns value, unstructured header text, with its encoded
// words decoded (RFC 2047), or as received when one does not decode,
// cleaned by [cleanText].
func decodeText(value string) string {
	decoded, err := wordDecoder.DecodeHeader(value)
	// A word in a charset go-message lacks, or malformed, fails the whole
	// value; inbok keeps the text as sent, which still reads in part,
	// rather than an empty subject.
	if err != nil {
		return cleanText(value)
	}
	return cleanText(decoded)
}

// parseAddresses returns the entries of value, an address list header (RFC 5322
// section 3.4), with encoded display names decoded, at most [maxAddresses]. A
// group's members are listed without the group. When the list does not parse as
// a whole, each entry is parsed alone, a group with its members, and one that
// is no address keeps its decoded text as the name.
func parseAddresses(value string) []mailsync.Address {
	// An empty or blank header names no one; net/mail would call it an error.
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var addresses []mailsync.Address
	value = unquoteEncodedWords(value)
	parsed, err := gomail.ParseAddressList(value)
	if err != nil {
		value = repairAddresses(value)
		parsed, err = gomail.ParseAddressList(value)
	}
	if err == nil {
		addresses = appendAddresses(addresses, parsed)
	} else {
		// net/mail refuses the whole list at its first bad entry; inbok parses the
		// entries one by one, so one bad entry loses only itself.
		for _, entry := range splitAddresses(value) {
			addresses = appendEntry(addresses, entry)
		}
	}
	if len(addresses) > maxAddresses {
		addresses = addresses[:maxAddresses]
	}
	return addresses
}

// appendAddresses appends parsed to addresses, cleaned by [cleanText].
func appendAddresses(addresses []mailsync.Address, parsed []*gomail.Address) []mailsync.Address {
	for _, a := range parsed {
		addresses = append(addresses, mailsync.Address{Name: cleanText(a.Name), Address: cleanText(a.Address)})
	}
	return addresses
}

// encodedWords matches text that is only RFC 2047 encoded words, separated
// by white space.
var encodedWords = regexp.MustCompile(`^\s*=\?[^?\s]+\?[bBqQ]\?[^?\s]*\?=(\s+=\?[^?\s]+\?[bBqQ]\?[^?\s]*\?=)*\s*$`)

// unquoteEncodedWords returns value with each quoted string that holds only
// encoded words written without its quotes, so net/mail decodes them as
// it does a phrase's, once. RFC 2047 section 5 forbids encoded words in a
// quoted string, so net/mail leaves "=?UTF-8?B?...?=" <a@x> as sent, but
// mailers write it, and enmime decodes it too. Decoding every name again
// after parsing instead would decode a name twice.
func unquoteEncodedWords(value string) string {
	if !strings.Contains(value, `"=?`) {
		return value
	}
	var out strings.Builder
	var s scanner
	start := -1 // where the current top-level quoted string opened
	for i := 0; i < len(value); i++ {
		c := value[i]
		wasQuoted := s.quoted
		opens := !wasQuoted && c == '"' && s.comments == 0 && !s.escaped
		s.skip(c)
		switch {
		case opens:
			start = i
		case wasQuoted && !s.quoted && start >= 0:
			quoted := value[start+1 : i]
			if encodedWords.MatchString(quoted) {
				out.WriteString(strings.TrimSpace(quoted))
			} else {
				out.WriteString(value[start : i+1])
			}
			start = -1
		case start < 0:
			out.WriteByte(c)
		}
	}
	if start >= 0 {
		out.WriteString(value[start:])
	}
	return out.String()
}

// repairAddresses returns value, an address list net/mail refused, with
// two breakages real mail has mended, as enmime's ParseAddressList mends
// them: white space inside angle brackets, which folding an address
// across lines leaves, is removed, and a comma is added where an address
// in angle brackets runs into the next entry, e.g. "B <b@x> c@x".
func repairAddresses(value string) string {
	var repaired strings.Builder
	var s scanner
	ended := false // just past an angle-addr, outside any token
	for i := 0; i < len(value); i++ {
		c := value[i]
		// Comments and white space may follow an address (RFC 5322
		// section 3.4), so the next entry starts at what comes after
		// them; a comma or a group's end already separates it.
		inToken := s.quoted || s.comments > 0
		if ended && !inToken && !isSpace(c) && c != '(' {
			if c != ',' && c != ';' {
				repaired.WriteString(", ")
			}
			ended = false
		}
		if s.skip(c) {
			repaired.WriteByte(c)
			continue
		}
		switch {
		case c == '<':
			s.angles++
		case c == '>' && s.angles > 0:
			s.angles--
			ended = s.angles == 0
		case s.angles > 0 && isSpace(c):
			continue
		}
		repaired.WriteByte(c)
	}
	return repaired.String()
}

// isSpace reports whether c is white space as RFC 5322 folds it: space,
// tab, or a line break.
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// appendEntry appends entry, one entry of an address list, to addresses: its
// addresses, several for a group, or its decoded text when it is no address.
func appendEntry(addresses []mailsync.Address, entry string) []mailsync.Address {
	parsed, err := gomail.ParseAddressList(entry)
	if err == nil {
		return appendAddresses(addresses, parsed)
	}
	// A group's bad member loses only itself, like a bad entry outside one.
	if members, ok := groupMembers(entry); ok {
		entries, _ := splitAt(members, groupsIgnored)
		for _, member := range entries {
			addresses = appendEntry(addresses, member)
		}
		return addresses
	}
	// inbok's choice, as [mailsync.Address] documents: the member still
	// sees what the sender wrote, e.g. a name without an address.
	return append(addresses, mailsync.Address{Name: decodeText(entry)})
}

// groupMembers returns the members of entry when it is a group, "name:
// members;" (RFC 5322 section 3.4), and whether it is one.
func groupMembers(entry string) (string, bool) {
	var s scanner
	for i := 0; i < len(entry); i++ {
		c := entry[i]
		if s.skip(c) {
			continue
		}
		switch {
		case c == '<':
			s.angles++
		case c == '>' && s.angles > 0:
			s.angles--
		case c == ':' && s.angles == 0:
			members, closed := strings.CutSuffix(strings.TrimSpace(entry[i+1:]), ";")
			return members, closed
		}
	}
	return "", false
}

// splitAddresses returns the entries of value, trimmed, without empty
// ones: split at its commas outside quoted strings, comments, angle
// brackets, and groups (RFC 5322 section 3.4). When a group never ends,
// e.g. for an unquoted colon in a name, it splits again ignoring groups,
// so the entries after it still parse.
func splitAddresses(value string) []string {
	entries, closed := splitAt(value, groupsKept)
	if !closed {
		entries, _ = splitAt(value, groupsIgnored)
	}
	return entries
}

// groupMode is whether [splitAt] keeps a group's commas inside one entry.
type groupMode int

const (
	groupsKept groupMode = iota
	groupsIgnored
)

// splitAt returns value's entries as [splitAddresses] describes, and
// whether every group it kept together ended.
func splitAt(value string, mode groupMode) ([]string, bool) {
	var entries []string
	var s scanner
	inGroup := false
	start := 0
	for i := 0; i < len(value); i++ {
		c := value[i]
		if s.skip(c) {
			continue
		}
		switch {
		case c == '<':
			s.angles++
		case c == '>' && s.angles > 0:
			s.angles--
		case s.angles > 0:
		case c == ':' && mode == groupsKept && !inGroup:
			inGroup = true
		case c == ';' && inGroup:
			inGroup = false
		case c == ',' && !inGroup:
			entries = appendTrimmed(entries, value[start:i])
			start = i + 1
		}
	}
	return appendTrimmed(entries, value[start:]), !inGroup
}

// appendTrimmed appends entry, trimmed, to entries unless it is empty.
func appendTrimmed(entries []string, entry string) []string {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return entries
	}
	return append(entries, entry)
}

// scanner tracks where a header scan stands in RFC 5322's lexical tokens:
// quoted strings and comments, each with its quoted-pairs (section 3.2),
// and angle brackets.
type scanner struct {
	quoted, escaped bool
	comments        int
	angles          int
}

// skip takes c, the next byte, and reports whether it is inside a quoted
// string or comment, or ends or escapes one, so the caller ignores it.
func (s *scanner) skip(c byte) bool {
	switch {
	case s.escaped:
		s.escaped = false
	case c == '\\' && (s.quoted || s.comments > 0):
		s.escaped = true
	case s.quoted:
		s.quoted = c != '"'
	case c == '"' && s.comments == 0:
		s.quoted = true
	case c == '(':
		s.comments++
	case c == ')' && s.comments > 0:
		s.comments--
	case s.comments > 0:
	default:
		return false
	}
	return true
}

// parseMsgIDs returns the msg-ids in value, each with its angle brackets,
// in order (RFC 5322 section 3.6.4), cleaned by [cleanText]. It skips
// comments, quoted strings, and any other text, which the obsolete
// In-Reply-To and References syntax allows as phrases (section 4.5.4).
// White space inside a bracketed token is removed: no msg-id holds any,
// but some mailers fold References inside one, as mox's ReferencedIDs
// notes, and unfolding leaves the space.
func parseMsgIDs(value string) []string {
	var ids []string
	var s scanner
	for i := 0; i < len(value); i++ {
		if s.skip(value[i]) || value[i] != '<' {
			continue
		}
		end, ok := msgIDEnd(value[i+1:])
		if !ok {
			return ids
		}
		if value[i+1+end] == '<' {
			continue
		}
		// strings.Fields copies, so the ID does not keep the header in memory.
		id := strings.Join(strings.Fields(value[i+1:i+1+end]), "")
		i += 1 + end
		// An empty <> has neither side of the @ section 3.6.4 requires. A
		// token without an @ is kept, inbok's choice: old mailers write
		// them, and an unmatched ID only threads nothing.
		if id != "" {
			ids = append(ids, cleanText("<"+id+">"))
		}
	}
	return ids
}

// msgIDEnd returns where the msg-id that rest follows its < in ends: the
// index of its >, or of a < that shows it was cut off, and whether either
// came. A quoted local part or a domain literal may hold < or > (RFC 5322
// section 3.6.4, with its obsolete forms in section 4.5.4), so those are
// skipped; when one never closes, the first < or > counts instead, so the
// IDs after a broken one are still read.
func msgIDEnd(rest string) (int, bool) {
	var quoted, escaped, literal bool
	for j := 0; j < len(rest); j++ {
		c := rest[j]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && (quoted || literal):
			escaped = true
		case quoted:
			quoted = c != '"'
		case literal:
			literal = c != ']'
		case c == '"':
			quoted = true
		case c == '[':
			literal = true
		case c == '<' || c == '>':
			return j, true
		}
	}
	end := strings.IndexAny(rest, "<>")
	return end, end >= 0
}

// parseReferences returns the msg-ids of value, a References header,
// trimmed by [msgid.TrimReferences] to the thread's start and the nearest
// parents. A value past [maxHeaderBytes] is read only at its start and end,
// which hold those.
func parseReferences(value string) []string {
	var ids []string
	if len(value) <= maxHeaderBytes {
		ids = parseMsgIDs(value)
	} else {
		// A msg-id cut at either edge is skipped: one cut at the end of
		// the start has no closing bracket, and one cut at the start of
		// the end no opening one.
		ids = parseMsgIDs(value[:maxHeaderBytes/2])
		ids = append(ids, parseMsgIDs(value[len(value)-maxHeaderBytes/2:])...)
	}
	return msgid.TrimReferences(ids)
}
