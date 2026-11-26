package outgoingmessage

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"
	"uuid"

	"inbok/internal/msgid"
)

// replyPrefix starts a reply's subject (RFC 5322 section 3.6.5).
const replyPrefix = "Re: "

// Reply is a reply a member asks to send from their account
// [Reply.ConnectedAccountID] to their synchronized message
// [Reply.AnsweredMessageID]. It has no subject: the reply takes the
// answered message's.
type Reply struct {
	ConnectedAccountID int64
	// IdempotencyKey is the client's key for this one reply, as
	// [Draft.IdempotencyKey].
	IdempotencyKey    uuid.UUID
	AnsweredMessageID uuid.UUID
	To                []string
	Cc                []string
	Bcc               []string
	Body              string
}

// Reply saves reply as userID's message, waiting to be sent, and returns it.
// Its subject is the answered message's, as replySubject words it, and its
// In-Reply-To and References continue the answered message's (RFC 5322
// section 3.6.4). Sent from the account that holds the answered message, it
// names that message's Gmail thread. A repeat of the reply's idempotency key
// returns the message the first saved, when it answers the same message with
// the same content from the same account.
//
// It returns [ErrAnsweredNotFound] when userID has no synchronized message
// [Reply.AnsweredMessageID], and the errors of [Service.Create] otherwise.
//
// DESIGN: another of the member's accounts has its own Gmail thread IDs, so a
// reply from it carries only the headers and starts a new thread in that
// account's Gmail, which does not thread by headers alone (observed on Gmail);
// recipients' mail clients thread it by References.
func (s *Service) Reply(ctx context.Context, userID int64, reply Reply) (Message, error) {
	answered := reply.AnsweredMessageID
	request := Request{AccountID: reply.ConnectedAccountID, Key: reply.IdempotencyKey, AnsweredID: &answered}
	message, err := s.create(ctx, userID, request, Content{To: reply.To, Cc: reply.Cc, Bcc: reply.Bcc, Body: reply.Body})
	if err != nil {
		return Message{}, err
	}
	return s.withAccount(ctx, userID, message)
}

// replyTo returns next, a message from sender, as a reply to answered: with
// answered's subject and threading, and in answered's Gmail thread when
// sender holds answered.
func replyTo(next NewMessage, sender Sender, answered Answered) NewMessage {
	next.Subject = replySubject(answered.Subject)
	next.Threading = replyThreading(answered)
	if sender.ConnectedAccountID == answered.ConnectedAccountID {
		next.ProviderThreadID = answered.ProviderThreadID
	}
	return next
}

// replySubject returns a reply's subject for the answered message's
// subject: subject with each control character as a space, a synchronized
// subject being decoded text that may hold characters a header line cannot,
// and with "Re: " before it (RFC 5322 section 3.6.5) unless it already
// starts with "Re:" in any case or the prefix would take it past 500
// characters. A subject longer than 500 characters is cut to 500.
//
// DESIGN: the prefix gives way to the subject, so the reply keeps the
// answered message's subject whole within inbok's limit, which a person
// hardly writes past; Gmail's thread guide asks for a matching subject
// without saying how it compares them. A "Re:" prefix, tabs, and line breaks
// leave RFC 5256's base subject unchanged (section 2.1); another control
// character turned into a space, or a cut, changes it.
func replySubject(subject string) string {
	subject = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, subject)
	length := utf8.RuneCountInString(subject)
	if length > maxSubjectLength {
		return string([]rune(subject)[:maxSubjectLength])
	}
	prefixed := len(subject) >= len(replyPrefix)-1 && strings.EqualFold(subject[:len(replyPrefix)-1], replyPrefix[:len(replyPrefix)-1])
	if prefixed || length+utf8.RuneCountInString(replyPrefix) > maxSubjectLength {
		return subject
	}
	return replyPrefix + subject
}

// replyThreading returns the threading of a reply to answered, as RFC 5322
// section 3.6.4 asks, counting a msg-id that is not [msgid.Writable] as
// missing: In-Reply-To is answered's Message-ID; References is answered's
// References, or else its In-Reply-To when that names one message, followed
// by its Message-ID, trimmed by [msgid.TrimReferences].
func replyThreading(answered Answered) Threading {
	references := writable(answered.References)
	if len(references) == 0 {
		if inReplyTo := writable(answered.InReplyTo); len(inReplyTo) == 1 && len(answered.InReplyTo) == 1 {
			references = inReplyTo
		}
	}
	var inReplyTo string
	if msgid.Writable(answered.InternetMessageID) {
		inReplyTo = answered.InternetMessageID
		references = append(references, inReplyTo)
	}
	return Threading{InReplyTo: inReplyTo, References: msgid.TrimReferences(references)}
}

// writable returns the msg-ids of ids that are [msgid.Writable], in order,
// never nil.
func writable(ids []string) []string {
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if msgid.Writable(id) {
			kept = append(kept, id)
		}
	}
	return kept
}
