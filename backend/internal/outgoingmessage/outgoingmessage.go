// Package outgoingmessage owns the messages members send from their
// connected accounts (US015). [Service] checks a member's draft, gives it a
// Message-ID, and saves it for the job that sends it, and reads back a
// member's own messages with their status.
package outgoingmessage

import (
	"context"
	"errors"
	"time"
	"uuid"
)

// Errors a caller shows the member. None includes an address or any other
// part of the message.
var (
	// ErrNotFound means the member has no message with that ID, including
	// one another member sent.
	ErrNotFound = errors.New("outgoing message not found")
	// ErrAccountNotFound means the member has no connected account with
	// that ID, including one another member connected.
	ErrAccountNotFound = errors.New("sending account not found")
	// ErrNeedsReconnect means Google refused the sending account's token,
	// so nothing is sent from it until the member reconnects it.
	ErrNeedsReconnect = errors.New("sending account needs reconnecting")
)

// Status is how far sending a message has got.
type Status string

// A message is saved waiting, then sending while a send runs, and ends sent,
// failed, or unknown.
const (
	// StatusWaiting is saved, with no send started.
	StatusWaiting Status = "waiting"
	// StatusSending is a send in progress, before Gmail answered.
	StatusSending Status = "sending"
	// StatusSent is accepted by Gmail.
	StatusSent Status = "sent"
	// StatusFailed is not sent, for the message's [Failure].
	StatusFailed Status = "failed"
	// StatusUnknown is a send that may have reached Gmail, with no answer
	// saying whether it did.
	StatusUnknown Status = "unknown"
)

// Failure is why a message ended [StatusFailed].
type Failure string

// Each failure asks something different of the member, so each is its own.
const (
	// FailureRefused is Gmail refusing the message, e.g. a recipient it
	// cannot deliver to.
	FailureRefused Failure = "refused"
	// FailureSendingLimit is Gmail's daily sending limit for the account,
	// which lasts hours.
	FailureSendingLimit Failure = "sending-limit"
	// FailureNeedsReconnect is Google refusing the account's token.
	FailureNeedsReconnect Failure = "needs-reconnect"
	// FailureTokenUnreadable is a token inbok can no longer decrypt, e.g. its
	// key was removed; reconnecting saves a new one.
	FailureTokenUnreadable Failure = "token-unreadable"
	// FailureDeactivated is the account's member being deactivated.
	FailureDeactivated Failure = "deactivated"
	// FailureSenderChanged is Gmail naming another address for the account
	// than the message's [Message.From], e.g. after Google renamed it.
	FailureSenderChanged Failure = "sender-changed"
	// FailureGaveUp is every attempt ending without the message sent, e.g.
	// Google unreachable for longer than the retries last.
	FailureGaveUp Failure = "gave-up"
)

// Content is what a member writes: recipients, subject, and a plain text
// body. A list with no addresses is empty, never nil.
type Content struct {
	To      []string
	Cc      []string
	Bcc     []string
	Subject string
	Body    string
}

// Draft is a message a member asks to send from their account
// [Draft.ConnectedAccountID].
type Draft struct {
	ConnectedAccountID int64
	Content
}

// Message is an outgoing message as saved.
type Message struct {
	// ID is inbok's own, random, so a URL that names the message tells
	// nothing of how many there are.
	ID uuid.UUID
	// ConnectedAccountID is the sending account, whose owner owns the message.
	ConnectedAccountID int64
	// From is the sending account's address when the message was saved,
	// kept though a reconnect may change the account's.
	From string
	Content
	// InbokMessageID is the ID inbok gave the message, an RFC 5322
	// Message-ID with its angle brackets, sent as its X-Inbok-Message-Id and
	// Message-ID headers. Gmail replaces the Message-ID; synchronized mail
	// keeps the one Gmail sent.
	InbokMessageID string
	Status         Status
	// Failure is set only when [Message.Status] is [StatusFailed].
	Failure Failure
	// ProviderMessageID and ProviderThreadID are Gmail's IDs for the message
	// and its thread, set once it is sent.
	ProviderMessageID string
	ProviderThreadID  string
	CreatedAt         time.Time
	// SentAt is when Gmail accepted the message; nil until then.
	SentAt *time.Time
}

// Sender is the account a message is sent from, as storage reads it.
type Sender struct {
	ConnectedAccountID int64
	Email              string
	// NeedsReconnect is whether Google refused the account's token.
	NeedsReconnect bool
}

// NewMessage is a message to save, before it has an ID.
type NewMessage struct {
	// From is the sending account's address.
	From string
	Content
	InbokMessageID string
}

// Storage keeps outgoing messages.
type Storage interface {
	// InsertOutgoingMessage reads userID's account accountID, keeping it
	// from being disconnected until the transaction ends, calls build with
	// it, and saves the message build returns as waiting, in one
	// transaction. It returns the saved message, or [ErrAccountNotFound]
	// when userID has no such account. An error from build rolls back and
	// is returned unchanged.
	InsertOutgoingMessage(ctx context.Context, userID, accountID int64, build func(Sender) (NewMessage, error)) (Message, error)

	// ReadOutgoingMessage returns userID's message messageID, or
	// [ErrNotFound] when userID has no such message.
	ReadOutgoingMessage(ctx context.Context, userID int64, messageID uuid.UUID) (Message, error)
}

// Service applies the rules for outgoing messages.
type Service struct {
	storage Storage
}

// NewService supplies storage.
func NewService(storage Storage) *Service {
	return &Service{storage: storage}
}

// Create saves draft as userID's message, waiting to be sent, and returns it.
// It returns [ErrAccountNotFound] when userID has no account
// draft.ConnectedAccountID, [ErrNeedsReconnect] when that account needs a
// reconnect, and the content errors of [Content]'s rules: [ErrNoRecipients],
// [ErrTooManyRecipients], a [*RecipientError], [ErrSubjectInvalid], or
// [ErrBodyInvalid].
func (s *Service) Create(ctx context.Context, userID int64, draft Draft) (Message, error) {
	// 1. Refuse content that cannot be sent before any storage.
	content, err := parseContent(draft.Content)
	if err != nil {
		return Message{}, err
	}

	// 2. With the account held, refuse one Google refused, and save.
	return s.storage.InsertOutgoingMessage(ctx, userID, draft.ConnectedAccountID, func(sender Sender) (NewMessage, error) {
		// A send would only fail, so the member hears it now and reconnects.
		if sender.NeedsReconnect {
			return NewMessage{}, ErrNeedsReconnect
		}
		messageID, err := newMessageID(sender.Email)
		if err != nil {
			return NewMessage{}, err
		}
		return NewMessage{From: sender.Email, Content: content, InbokMessageID: messageID}, nil
	})
}

// Get returns userID's message messageID, or [ErrNotFound] when userID has
// no such message.
func (s *Service) Get(ctx context.Context, userID int64, messageID uuid.UUID) (Message, error) {
	return s.storage.ReadOutgoingMessage(ctx, userID, messageID)
}
