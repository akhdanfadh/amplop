package smtp

import (
	"cmp"
	"encoding/base64"
	"errors"
	"net/textproto"
	"regexp"
	"slices"
	"strings"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
)

// errGarbledReply stands for a malformed reply after the sign-in, whose
// text inbok does not keep.
var errGarbledReply = errors.New("server sent a reply that cannot be read")

// recordingClient is a SASL client that records each response it gives,
// Base64-encoded as SMTP sends it (RFC 4954).
type recordingClient struct {
	sasl.Client
	sent []string
}

// Start starts the wrapped mechanism and records its initial response.
func (c *recordingClient) Start() (string, []byte, error) {
	mech, response, err := c.Client.Start()
	c.record(response)
	return mech, response, err
}

// Next answers challenge with the wrapped mechanism and records the response.
func (c *recordingClient) Next(challenge []byte) ([]byte, error) {
	response, err := c.Client.Next(challenge)
	c.record(response)
	return response, err
}

// record adds response, Base64-encoded, to c.sent, unless it is empty.
func (c *recordingClient) record(response []byte) {
	if len(response) > 0 {
		c.sent = append(c.sent, base64.StdEncoding.EncodeToString(response))
	}
}

// redaction holds what inbok sent that a later reply must not carry into
// an error: the sign-in's Base64 lines and the recipient's address.
type redaction struct {
	credentials []string
	recipient   string
}

// redact returns err with each sign-in line replaced by [credentials] and
// the recipient's address, in any letter case, by [recipient]. A refusal
// stays an [*gosmtp.SMTPError] with its codes, a malformed reply becomes
// [errGarbledReply], and any other err, whose text is Go's or inbok's, is
// returned as is.
//
// DESIGN: the mailer logs a reply that is not private, so an administrator
// can see why a send failed, but a reply can quote what it answers. A server
// out of step with the session, e.g. a proxy, can answer MAIL by quoting the
// AUTH line, and a refused recipient is usually quoted in full. Replacing
// only those strings keeps the rest of the reply. Dropping every reply after
// the sign-in would also close the leak, but loses the text that explains a
// refused sender or recipient.
func (r redaction) redact(err error) error {
	// go-smtp documents a server's error as an *SMTPError, which failed and
	// Send label by, so a refusal keeps its type and codes.
	if smtpErr, ok := errors.AsType[*gosmtp.SMTPError](err); ok {
		redacted := *smtpErr
		redacted.Message = r.text(smtpErr.Message)
		return &redacted
	}
	// Go quotes a malformed reply with escapes, e.g. \ue000, which matching
	// the strings inbok sent can miss, so none of its text is kept.
	if _, ok := errors.AsType[textproto.ProtocolError](err); ok {
		return errGarbledReply
	}
	// Only those two carry the server's text. Rewriting another, e.g. a
	// network error naming the server's address, would cost refused and
	// Send its type.
	return err
}

// text returns s with each match of r's strings replaced: a range where
// matches overlap or touch is replaced once, by [credentials] when a
// sign-in line is in it and by [recipient] otherwise.
func (r redaction) text(s string) string {
	// 1. Find every match in the original text. Replacing one string at a time
	// would let an earlier replacement break a later match, e.g. LOGIN's
	// username line inside its password line, and leave part of it.
	type match struct {
		start, end  int
		credentials bool
	}
	var matches []match
	for _, line := range r.credentials {
		for at := 0; ; at++ {
			found := strings.Index(s[at:], line)
			if found < 0 {
				break
			}
			at += found
			matches = append(matches, match{at, at + len(line), true})
		}
	}
	if r.recipient != "" {
		// Case-insensitive, since a server may quote the address in its own case.
		recipient := regexp.MustCompile("(?i)" + regexp.QuoteMeta(r.recipient))
		for _, found := range recipient.FindAllStringIndex(s, -1) {
			matches = append(matches, match{found[0], found[1], false})
		}
	}
	if len(matches) == 0 {
		return s
	}
	slices.SortFunc(matches, func(a, b match) int { return cmp.Compare(a.start, b.start) })

	// 2. Merge the ranges, then replace each merged range.
	var out strings.Builder
	done := 0
	for i := 0; i < len(matches); {
		merged := matches[i]
		for i++; i < len(matches) && matches[i].start <= merged.end; i++ {
			merged.end = max(merged.end, matches[i].end)
			merged.credentials = merged.credentials || matches[i].credentials
		}
		out.WriteString(s[done:merged.start])
		if merged.credentials {
			out.WriteString("[credentials]")
		} else {
			out.WriteString("[recipient]")
		}
		done = merged.end
	}
	out.WriteString(s[done:])
	return out.String()
}
