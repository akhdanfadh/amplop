package outgoingmessage

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"inbok/internal/emailaddress"
)

// Limits on a message's content, all inbok's choices.
const (
	// MaxRecipients bounds each of To, Cc, and Bcc, so a limit belongs to one
	// field a form can mark; 300 in all stays under the 500 Google allows a
	// message sent through the Gmail API.
	MaxRecipients = 100
	// maxSubjectLength bounds a subject in characters, longer than any a
	// person writes.
	maxSubjectLength = 500
	// maxBodyLength bounds a body in characters: at most 4 bytes each, or 6
	// as a JSON escape, it fits the API's 1 MB request body.
	maxBodyLength = 100_000
)

// Errors in a message's content. None includes any part of the content.
var (
	// ErrNoRecipients means To, Cc, and Bcc are all empty.
	ErrNoRecipients = errors.New("the message has no recipients")
	// ErrTooManyRecipients means To, Cc, or Bcc holds more than 100
	// addresses; a [*TooManyRecipientsError] says which.
	ErrTooManyRecipients = errors.New("a recipient list has too many addresses")
	// ErrRecipientInvalid means a recipient is not one bare email address;
	// a [*RecipientError] says which.
	ErrRecipientInvalid = errors.New("a recipient is not a valid email address")
	// ErrSubjectInvalid means the subject is longer than 500 characters or
	// holds a line break or another control character.
	ErrSubjectInvalid = errors.New("the subject is too long or holds a control character")
	// ErrBodyInvalid means the body is longer than 100,000 characters or
	// holds a NUL character.
	ErrBodyInvalid = errors.New("the body is too long or holds a NUL character")
)

// Field is one of a message's recipient lists.
type Field string

const (
	FieldTo  Field = "to"
	FieldCc  Field = "cc"
	FieldBcc Field = "bcc"
)

// RecipientError is the recipient at [RecipientError.Index] in
// [RecipientError.Field] that is not one bare email address, as
// [emailaddress.ParseRecipient] decides. It matches [ErrRecipientInvalid].
type RecipientError struct {
	Field Field
	Index int
}

// Error names no address.
func (e *RecipientError) Error() string {
	return ErrRecipientInvalid.Error()
}

// Is reports whether target is [ErrRecipientInvalid].
func (e *RecipientError) Is(target error) bool {
	return target == ErrRecipientInvalid
}

// TooManyRecipientsError is [TooManyRecipientsError.Field] holding more than
// 100 addresses. It matches [ErrTooManyRecipients].
type TooManyRecipientsError struct {
	Field Field
}

// Error names no address.
func (e *TooManyRecipientsError) Error() string {
	return ErrTooManyRecipients.Error()
}

// Is reports whether target is [ErrTooManyRecipients].
func (e *TooManyRecipientsError) Is(target error) bool {
	return target == ErrTooManyRecipients
}

// parseContent returns content with each recipient as
// [emailaddress.ParseRecipient] spells it and each empty list non-nil, or
// the first error in the order: recipients, subject, body.
func parseContent(content Content) (Content, error) {
	// Count before parsing, so a long list costs no parsing.
	lists := []struct {
		field     Field
		addresses []string
	}{{FieldTo, content.To}, {FieldCc, content.Cc}, {FieldBcc, content.Bcc}}
	for _, list := range lists {
		if len(list.addresses) > MaxRecipients {
			return Content{}, &TooManyRecipientsError{Field: list.field}
		}
	}
	to, err := parseRecipients(FieldTo, content.To)
	if err != nil {
		return Content{}, err
	}
	cc, err := parseRecipients(FieldCc, content.Cc)
	if err != nil {
		return Content{}, err
	}
	bcc, err := parseRecipients(FieldBcc, content.Bcc)
	if err != nil {
		return Content{}, err
	}
	if len(to)+len(cc)+len(bcc) == 0 {
		return Content{}, ErrNoRecipients
	}
	if utf8.RuneCountInString(content.Subject) > maxSubjectLength || !subjectSafe(content.Subject) {
		return Content{}, ErrSubjectInvalid
	}
	// PostgreSQL's text cannot hold NUL.
	if utf8.RuneCountInString(content.Body) > maxBodyLength || strings.ContainsRune(content.Body, 0) {
		return Content{}, ErrBodyInvalid
	}
	return Content{To: to, Cc: cc, Bcc: bcc, Subject: content.Subject, Body: content.Body}, nil
}

// subjectSafe reports whether subject can be one header line: it holds no
// line break or other control character, which a subject field cannot type
// either (RFC 5322).
func subjectSafe(subject string) bool {
	return !strings.ContainsFunc(subject, unicode.IsControl)
}

// parseRecipients returns addresses, field's list, each as
// [emailaddress.ParseRecipient] spells it, or a [*RecipientError] for the
// first it refuses.
func parseRecipients(field Field, addresses []string) ([]string, error) {
	parsed := make([]string, 0, len(addresses))
	for i, address := range addresses {
		recipient, err := emailaddress.ParseRecipient(address)
		if err != nil {
			return nil, &RecipientError{Field: field, Index: i}
		}
		parsed = append(parsed, recipient)
	}
	return parsed, nil
}
