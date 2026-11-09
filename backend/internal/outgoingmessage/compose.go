package outgoingmessage

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/emersion/go-message/mail"

	"inbok/internal/emailaddress"
)

// fallbackMessageIDDomain stands in for a sending domain that cannot be a
// Message-ID's right side.
const fallbackMessageIDDomain = "localhost"

// maxEncodedWordText is the most UTF-8 bytes one base64 encoded word carries:
// RFC 2047 limits a word to 75 characters, of which "=?utf-8?b?" and "?="
// take 12, leaving 63, and 60 base64 characters, the most that fit in
// whole groups of 4, hold 45 bytes.
const maxEncodedWordText = 45

// errUnsafeHeader means the From address, subject, or Message-ID could add
// a header or is not one value.
var errUnsafeHeader = errors.New("the sender, subject, or Message-ID cannot be written as a header")

// compose returns message as an RFC 5322 message: From [Message.From], To,
// Cc, and Bcc when not empty, Subject, Date when the member sent it,
// Message-ID, and the body as quoted-printable UTF-8 plain text. The same
// message always gives the same bytes. It returns an error, and no bytes,
// for a value that cannot be written as a header: a [*RecipientError], or
// [errUnsafeHeader] for From, the subject, or the Message-ID. It does not
// apply [Content]'s limits, which decided what inbok accepted.
func compose(message Message) ([]byte, error) {
	// 1. Check each value that becomes a header, wherever the message came
	// from: a stray character there starts another header line, e.g. a Bcc
	// the member never chose. The body cannot, as quoted-printable encodes
	// every character.
	to, err := parseRecipients(FieldTo, message.To)
	if err != nil {
		return nil, err
	}
	cc, err := parseRecipients(FieldCc, message.Cc)
	if err != nil {
		return nil, err
	}
	bcc, err := parseRecipients(FieldBcc, message.Bcc)
	if err != nil {
		return nil, err
	}
	from, err := emailaddress.ParseRecipient(message.From)
	if err != nil {
		return nil, errUnsafeHeader
	}
	if !subjectSafe(message.Subject) || !messageIDSafe(message.InternetMessageID) {
		return nil, errUnsafeHeader
	}

	// 2. Write the header. go-message encodes non-ASCII text as RFC 2047
	// words and folds long lines.
	var header mail.Header
	header.SetAddressList("From", []*mail.Address{{Address: from}})
	setAddresses(&header, "To", to)
	setAddresses(&header, "Cc", cc)
	// Gmail delivers to the Bcc header's addresses (messages.send).
	setAddresses(&header, "Bcc", bcc)
	header.Set("Subject", subjectText(message.Subject))
	// UTC, as pgx reads a time in the worker's zone, so every worker writes
	// the same Date.
	header.SetDate(message.CreatedAt.UTC())
	header.Set("Message-Id", message.InternetMessageID)
	header.SetContentType("text/plain", map[string]string{"charset": "utf-8"})

	// 3. Write the body, which go-message encodes as quoted-printable.
	var out bytes.Buffer
	writer, err := mail.CreateSingleInlineWriter(&out, header)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(writer, message.Body); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// subjectText returns subject as a Subject header's text: go-message's
// RFC 2047 words for what needs them, or base64 words for all of subject
// when it holds "=?". A reader would otherwise decode text that only looks
// like an encoded word, which Go's encoder leaves as it is.
func subjectText(subject string) string {
	if !strings.Contains(subject, "=?") {
		var header mail.Header
		header.SetSubject(subject)
		return header.Get("Subject")
	}
	var words []string
	for subject != "" {
		// Cut at a character's start, so each word holds whole characters
		// (RFC 2047, section 5).
		n := min(len(subject), maxEncodedWordText)
		for n < len(subject) && !utf8.RuneStart(subject[n]) {
			n--
		}
		words = append(words, "=?utf-8?b?"+base64.StdEncoding.EncodeToString([]byte(subject[:n]))+"?=")
		subject = subject[n:]
	}
	// White space between encoded words is dropped when decoding (RFC 2047,
	// section 6.2), and the header writer folds at it.
	return strings.Join(words, " ")
}

// setAddresses sets header's field key to addresses, or leaves it out when
// there are none.
func setAddresses(header *mail.Header, key string, addresses []string) {
	if len(addresses) == 0 {
		return
	}
	list := make([]*mail.Address, 0, len(addresses))
	for _, address := range addresses {
		list = append(list, &mail.Address{Address: address})
	}
	header.SetAddressList(key, list)
}

// newMessageID returns a new Message-ID, with its angle brackets, for a
// message sent from email: random on the left, email's domain on the right,
// or [fallbackMessageIDDomain] when that domain is not an ASCII dot-atom.
func newMessageID(email string) (string, error) {
	// The sender's own domain names no inbok host to the recipients, and the
	// message already carries it in From.
	domain := email[strings.LastIndexByte(email, '@')+1:]
	if domain == email || !dotAtom(domain) {
		domain = fallbackMessageIDDomain
	}
	var header mail.Header
	if err := header.GenerateMessageIDWithHostname(domain); err != nil {
		return "", err
	}
	return header.Get("Message-Id"), nil
}

// messageIDSafe reports whether id is a Message-ID as [newMessageID] makes
// one: a dot-atom, "@", and a dot-atom, in angle brackets (RFC 5322,
// section 3.6.4), so it is one header value.
func messageIDSafe(id string) bool {
	inner, ok := strings.CutPrefix(id, "<")
	if !ok {
		return false
	}
	inner, ok = strings.CutSuffix(inner, ">")
	left, right, found := strings.Cut(inner, "@")
	return ok && found && dotAtom(left) && dotAtom(right)
}

// dotAtom reports whether s is ASCII dot-atom text: letters, digits, and the
// other atext characters, in dot-separated parts that are not empty
// (RFC 5322, section 3.2.3).
func dotAtom(s string) bool {
	for part := range strings.SplitSeq(s, ".") {
		if part == "" || strings.ContainsFunc(part, func(r rune) bool { return !atext(r) }) {
			return false
		}
	}
	return true
}

// atext reports whether r is an RFC 5322 atext character.
func atext(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9':
		return true
	}
	return strings.ContainsRune("!#$%&'*+-/=?^_`{|}~", r)
}
