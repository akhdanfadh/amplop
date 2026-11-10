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

	"inbok/internal/attemptlimit"
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
	// ErrDeactivated means the member was deactivated, so they send nothing.
	ErrDeactivated = errors.New("sending member is deactivated")
	// ErrThrottled means the member asked to send too many messages recently;
	// it comes wrapped in an [*attemptlimit.Error] that says how long to wait.
	ErrThrottled = errors.New("too many messages sent recently")
)

// The member's send limit counts every message saved to send, from any of
// their accounts (ADR006).
//
// DESIGN: Gmail limits each account, but not what the members of one
// instance share: its Google client's quota and standing, and the send
// queue. A person writing by hand stays far below the short window, while a
// runaway client is stopped within seconds and then held to 2 a minute. The
// long window stays under the 500 messages a day Gmail allows a personal
// account, even all from one account, so one member's loop is stopped
// before Gmail blocks the account for up to a day, in Gmail itself too. It
// is no guarantee: Gmail counts the account's mail from every client, and
// several members may connect one Google account.
var (
	shortSendLimit = attemptlimit.Limit{Window: 15 * time.Minute, Max: 30}
	longSendLimit  = attemptlimit.Limit{Window: 24 * time.Hour, Max: 200}
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

// Attempt is a member's request to send, which the send limit counts.
type Attempt struct {
	UserID int64
	// At is when the member asked.
	At time.Time
	// Since is where the limit's longest window starts: earlier attempts no
	// longer count.
	Since time.Time
}

// CreateState is what storage reads, under the member's lock, for a new
// message.
type CreateState struct {
	Sender Sender
	// Attempts are the times of the member's attempts after [Attempt.Since].
	Attempts []time.Time
}

// Storage keeps outgoing messages.
type Storage interface {
	// InsertOutgoingMessage, in one transaction:
	//  1. locks the member attempt.UserID, so their attempts are counted one
	//     at a time, returning [ErrDeactivated] when they are deactivated;
	//  2. reads the member's account accountID, keeping it from being
	//     disconnected until the transaction ends, or returns
	//     [ErrAccountNotFound];
	//  3. calls build with the account and the member's attempts after
	//     attempt.Since;
	//  4. saves the message build returns as waiting, with the job that sends
	//     it, so no message waits without a job, and an attempt at
	//     attempt.At, deleting the member's attempts at or before
	//     attempt.Since.
	//
	// It returns the saved message. An error from build rolls back and is
	// returned unchanged.
	InsertOutgoingMessage(ctx context.Context, attempt Attempt, accountID int64, build func(CreateState) (NewMessage, error)) (Message, error)

	// ReadOutgoingMessage returns userID's message messageID, or
	// [ErrNotFound] when userID has no such message.
	ReadOutgoingMessage(ctx context.Context, userID int64, messageID uuid.UUID) (Message, error)
}

// Service applies the rules for outgoing messages.
type Service struct {
	storage Storage
	now     func() time.Time
}

// NewService supplies storage and the clock the send limit counts by.
func NewService(storage Storage, now func() time.Time) *Service {
	return &Service{storage: storage, now: now}
}

// Create saves draft as userID's message, waiting to be sent, and returns it.
//
// It returns [ErrAccountNotFound] when userID has no account
// draft.ConnectedAccountID, [ErrNeedsReconnect] when that account needs a
// reconnect, [ErrDeactivated], an [*attemptlimit.Error] wrapping
// [ErrThrottled], and the content errors of [Content]'s rules:
// [ErrNoRecipients], a [*TooManyRecipientsError], a [*RecipientError],
// [ErrSubjectInvalid], or [ErrBodyInvalid].
func (s *Service) Create(ctx context.Context, userID int64, draft Draft) (Message, error) {
	// 1. Refuse content that cannot be sent before any storage.
	content, err := parseContent(draft.Content)
	if err != nil {
		return Message{}, err
	}

	// 2. With the member and account held, refuse what would only fail or
	// go over the limit, and save.
	attempt := s.attempt(userID)
	return s.storage.InsertOutgoingMessage(ctx, attempt, draft.ConnectedAccountID, func(state CreateState) (NewMessage, error) {
		return newMessage(state.Sender, state.Attempts, attempt.At, content)
	})
}

// Get returns userID's message messageID, or [ErrNotFound] when userID has
// no such message.
func (s *Service) Get(ctx context.Context, userID int64, messageID uuid.UUID) (Message, error) {
	return s.storage.ReadOutgoingMessage(ctx, userID, messageID)
}

// attempt returns userID's attempt to send now.
func (s *Service) attempt(userID int64) Attempt {
	now := s.now().UTC()
	return Attempt{UserID: userID, At: now, Since: now.Add(-longSendLimit.Window)}
}

// newMessage returns content as a message to save from sender, or
// [ErrNeedsReconnect] when Google refused sender's token, or an
// [*attemptlimit.Error] wrapping [ErrThrottled] when the member's attempts
// leave no room at now.
func newMessage(sender Sender, attempts []time.Time, now time.Time, content Content) (NewMessage, error) {
	// A send would only fail, so the member hears it now and reconnects.
	if sender.NeedsReconnect {
		return NewMessage{}, ErrNeedsReconnect
	}
	if wait := attemptlimit.Wait(attempts, now, shortSendLimit, longSendLimit); wait > 0 {
		return NewMessage{}, &attemptlimit.Error{Err: ErrThrottled, Wait: wait}
	}
	messageID, err := newMessageID(sender.Email)
	if err != nil {
		return NewMessage{}, err
	}
	return NewMessage{From: sender.Email, Content: content, InbokMessageID: messageID}, nil
}
