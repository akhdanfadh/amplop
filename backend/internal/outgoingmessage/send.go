package outgoingmessage

import (
	"context"
	"errors"

	"inbok/internal/connectedaccount"
)

// Errors a [Provider] returns. None includes an address or any other part
// of the message.
var (
	// ErrRefused means Gmail answered that it will not send the message,
	// e.g. a recipient it cannot deliver to.
	ErrRefused = errors.New("gmail refused the message")
	// ErrSendingLimit means Gmail answered that the account reached its daily
	// sending limit, which lasts hours.
	ErrSendingLimit = errors.New("the account reached gmail's sending limit")
	// ErrNotSent means the message did not reach Gmail, or Gmail answered
	// that it did not send it, and trying again may work: e.g. no connection,
	// a rate limit, or a token Gmail did not accept.
	ErrNotSent = errors.New("the message was not sent")
	// ErrUnanswered means the request may have reached Gmail and no answer
	// says whether Gmail sent it: e.g. the connection dropped, the answer
	// timed out, or Gmail answered with a server error.
	ErrUnanswered = errors.New("the message may have been sent")
	// ErrSentNotFound means no message Gmail added to Sent since the given
	// position carries the ID.
	ErrSentNotFound = errors.New("no sent message carries the id")
	// ErrCheckIncomplete means a check could not read every message Gmail
	// added to Sent since the given position, e.g. one was deleted, or
	// Gmail no longer keeps changes that old.
	ErrCheckIncomplete = errors.New("the check could not read every sent message")
)

// Mailbox is what Gmail says of the mailbox an access token reaches.
type Mailbox struct {
	// Email is the mailbox's address now.
	Email string
	// HistoryID is Gmail's position in the mailbox's changes.
	HistoryID string
}

// Sent is Gmail's IDs for a message it accepted.
type Sent struct {
	ProviderMessageID string
	ProviderThreadID  string
}

// Provider is Gmail as sending needs it. Each call takes an access token for
// the sending account.
type Provider interface {
	// Mailbox returns the mailbox token reaches.
	Mailbox(ctx context.Context, token connectedaccount.AccessToken) (Mailbox, error)

	// Send sends raw, an RFC 5322 message, from the mailbox. When the message is
	// not known to be sent, it returns only [ErrRefused], [ErrSendingLimit],
	// [ErrNotSent], or [ErrUnanswered], and [ErrNotSent] only when Gmail said so
	// or no part of the request can have reached it.
	Send(ctx context.Context, token connectedaccount.AccessToken, raw []byte) (Sent, error)

	// FindSent returns the message Gmail added to Sent after historyID
	// whose X-Inbok-Message-Id is inbokMessageID. It returns
	// [ErrSentNotFound] when it read every such message and none matched,
	// and [ErrCheckIncomplete] when it could not read them all.
	FindSent(ctx context.Context, token connectedaccount.AccessToken, historyID, inbokMessageID string) (Sent, error)
}
