// Package outgoingmessage owns the messages members send from their
// connected accounts (US015). [Service] checks a member's draft, gives it a
// Message-ID, and saves it for the job that sends it, sends a failed message
// again as a copy, and reads back a member's own messages with their status.
package outgoingmessage

import (
	"context"
	"errors"
	"slices"
	"time"
	"uuid"

	"inbok/internal/attemptlimit"
	"inbok/internal/connectedaccount"
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
	// ErrTokenUnreadable means no configured key opens the sending account's
	// saved token, so nothing is sent from it until the member reconnects it
	// or an administrator restores the key.
	ErrTokenUnreadable = errors.New("sending account's token is unreadable")
	// ErrDeactivated means the member was deactivated, so they send nothing.
	ErrDeactivated = errors.New("sending member is deactivated")
	// ErrThrottled means the member asked to send too many messages recently;
	// it comes wrapped in an [*attemptlimit.Error] that says how long to wait.
	ErrThrottled = errors.New("too many messages sent recently")
	// ErrKeyReused means a request's idempotency key already saved another
	// of the member's messages: other content, or another sending account.
	ErrKeyReused = errors.New("idempotency key already used for another message")
	// ErrNotSendableAgain means the message is waiting, sending, or sent, so
	// sending it again could send it twice.
	ErrNotSendableAgain = errors.New("only a failed or unknown message can be sent again")
)

// ErrKeyExists is what [Storage] returns when the member already saved a
// message with the idempotency key; [Service] reads it to compare.
var ErrKeyExists = errors.New("a message with this idempotency key exists")

// The member's send limit counts every message saved to send, from any of
// their accounts, including one sent again (ADR006).
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

// maxListed bounds [Service.List]: the newest messages, enough to find the
// ones that ended without being sent.
const maxListed = 50

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
	// IdempotencyKey is the client's key for this one draft, the same for
	// every request that tries to send it, so a repeat saves no second
	// message.
	IdempotencyKey uuid.UUID
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
	// SentAgainAs is the copy the member sent again of this failed or
	// unknown message; nil until then.
	SentAgainAs *uuid.UUID
}

// Sender is the account a message is sent from, as storage reads it.
type Sender struct {
	ConnectedAccountID int64
	Email              string
	// NeedsReconnect is whether Google refused the account's token.
	NeedsReconnect bool
	// Credential is the account's sealed refresh token, only to check that a
	// key still opens it.
	Credential connectedaccount.Credential
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

// CopyState is what storage reads, under the member's lock, for a message
// sent again.
type CopyState struct {
	// Original is the message to send again.
	Original Message
	// Sender is the original's account as it is now.
	Sender Sender
	// Attempts are the times of the member's attempts after [Attempt.Since].
	Attempts []time.Time
}

// Storage keeps outgoing messages.
type Storage interface {
	// InsertOutgoingMessage, in one transaction:
	//  1. locks the member attempt.UserID, so their attempts are counted one
	//     at a time, returning [ErrDeactivated] when they are deactivated;
	//  2. returns [ErrKeyExists] when the member saved a message with key;
	//  3. reads the member's account accountID, keeping it from being
	//     disconnected until the transaction ends, or returns
	//     [ErrAccountNotFound];
	//  4. calls build with the account and the member's attempts after
	//     attempt.Since;
	//  5. saves the message build returns as waiting, with the job that sends
	//     it, so no message waits without a job, an attempt at attempt.At,
	//     deleting the member's attempts at or before attempt.Since, and key,
	//     returning [ErrKeyExists] if another transaction saved it first.
	//
	// It returns the saved message. An error from build rolls back and is
	// returned unchanged.
	InsertOutgoingMessage(ctx context.Context, attempt Attempt, accountID int64, key uuid.UUID, build func(CreateState) (NewMessage, error)) (Message, error)

	// InsertOutgoingMessageCopy, in one transaction:
	//  1. locks the member attempt.UserID, returning [ErrDeactivated] when
	//     they are deactivated;
	//  2. reads the member's message messageID, or returns [ErrNotFound];
	//  3. returns the copy already sent again, when there is one, without
	//     calling build;
	//  4. reads the message's account, keeping it from being disconnected,
	//     then locks the message; either gone returns [ErrNotFound];
	//  5. calls build with both and the member's attempts after
	//     attempt.Since;
	//  6. saves the copy build returns as waiting, with the job that sends
	//     it, the original's link to it, and an attempt as
	//     [Storage.InsertOutgoingMessage] does, but no key.
	//
	// It returns the copy. An error from build rolls back and is returned
	// unchanged.
	InsertOutgoingMessageCopy(ctx context.Context, attempt Attempt, messageID uuid.UUID, build func(CopyState) (NewMessage, error)) (Message, error)

	// ReadOutgoingMessage returns userID's message messageID, or
	// [ErrNotFound] when userID has no such message.
	ReadOutgoingMessage(ctx context.Context, userID int64, messageID uuid.UUID) (Message, error)

	// ReadOutgoingMessageByKey returns userID's message saved with key, or
	// [ErrNotFound] when userID has none.
	ReadOutgoingMessageByKey(ctx context.Context, userID int64, key uuid.UUID) (Message, error)

	// ListOutgoingMessages returns userID's newest messages, at most limit,
	// newest first, each with an empty [Content.Body].
	ListOutgoingMessages(ctx context.Context, userID int64, limit int) ([]Message, error)
}

// tokenChecker tells whether a configured key opens an account's saved
// refresh token; [connectedaccount.Service] does.
type tokenChecker interface {
	// CheckToken returns an error wrapping
	// [connectedaccount.ErrTokenUnreadable] when no key opens credential,
	// the token of userID's account accountID.
	CheckToken(userID, accountID int64, credential connectedaccount.Credential) error
}

// Service applies the rules for outgoing messages.
type Service struct {
	storage Storage
	tokens  tokenChecker
	now     func() time.Time
}

// NewService supplies storage, what checks a sending account's token, and
// the clock the send limit counts by.
func NewService(storage Storage, tokens tokenChecker, now func() time.Time) *Service {
	return &Service{storage: storage, tokens: tokens, now: now}
}

// Create saves draft as userID's message, waiting to be sent, and returns it.
// A repeat of a draft's idempotency key returns the message the first saved,
// when it asks for the same content from the same account.
//
// It returns [ErrAccountNotFound] when userID has no account
// draft.ConnectedAccountID, [ErrNeedsReconnect] when that account needs a
// reconnect, [ErrTokenUnreadable] when no key opens its token,
// [ErrKeyReused] when the key saved another message, [ErrDeactivated], an
// [*attemptlimit.Error] wrapping [ErrThrottled], and the content errors of
// [Content]'s rules: [ErrNoRecipients], a [*TooManyRecipientsError], a
// [*RecipientError], [ErrSubjectInvalid], or [ErrBodyInvalid].
func (s *Service) Create(ctx context.Context, userID int64, draft Draft) (Message, error) {
	// 1. Refuse content that cannot be sent before any storage.
	content, err := parseContent(draft.Content)
	if err != nil {
		return Message{}, err
	}

	attempt := s.attempt(userID)
	for tries := 1; ; tries++ {
		// 2. With the member and account held, refuse what would only fail
		// or go over the limit, and save.
		message, err := s.storage.InsertOutgoingMessage(ctx, attempt, draft.ConnectedAccountID, draft.IdempotencyKey, func(state CreateState) (NewMessage, error) {
			if err := s.checkSender(userID, state.Sender); err != nil {
				return NewMessage{}, err
			}
			return newMessage(state.Sender, state.Attempts, attempt.At, content)
		})
		if !errors.Is(err, ErrKeyExists) {
			return message, err
		}

		// 3. A repeated request, e.g. one whose answer was lost, gets the
		// message the first saved; the key's check comes before the limit's,
		// so a repeat is never refused for the attempt it repeats.
		message, err = s.repeated(ctx, userID, draft.ConnectedAccountID, draft.IdempotencyKey, content)
		// A disconnect that deleted the key's message since step 2 freed the
		// key, so the request is a new one, as it would have been a moment
		// later. Once: freeing it again needs the member to save and
		// disconnect it meanwhile.
		if !errors.Is(err, errKeyFreed) || tries == 2 {
			return message, err
		}
	}
}

// errKeyFreed means the message an idempotency key saved was deleted after
// the key was found, which deleted the key too.
var errKeyFreed = errors.New("idempotency key's message was deleted")

// repeated returns userID's message saved with key when it is from account
// accountID with content, [ErrKeyReused], or errKeyFreed when the message is
// gone.
func (s *Service) repeated(ctx context.Context, userID, accountID int64, key uuid.UUID, content Content) (Message, error) {
	earlier, err := s.storage.ReadOutgoingMessageByKey(ctx, userID, key)
	if errors.Is(err, ErrNotFound) {
		return Message{}, errKeyFreed
	}
	if err != nil {
		return Message{}, err
	}
	// The IETF Idempotency-Key draft refuses a key reused with another
	// payload (draft-ietf-httpapi-idempotency-key-header, section 2.2).
	if earlier.ConnectedAccountID != accountID || !sameContent(earlier.Content, content) {
		return Message{}, ErrKeyReused
	}
	return earlier, nil
}

// SendAgain saves a copy of userID's failed or unknown message messageID,
// waiting to be sent from the same account with a new ID and Message-ID, and
// returns it. Sending a message again a second time returns the same copy.
//
// It returns [ErrNotFound] when userID has no such message,
// [ErrNotSendableAgain] when it did not end failed or unknown,
// [ErrNeedsReconnect] when its account needs a reconnect,
// [ErrTokenUnreadable] when no key opens its token, [ErrDeactivated],
// or an [*attemptlimit.Error] wrapping [ErrThrottled].
func (s *Service) SendAgain(ctx context.Context, userID int64, messageID uuid.UUID) (Message, error) {
	attempt := s.attempt(userID)
	return s.storage.InsertOutgoingMessageCopy(ctx, attempt, messageID, func(state CopyState) (NewMessage, error) {
		// Waiting or sending would make two sends of one message, and a sent
		// message reached its recipients.
		if state.Original.Status != StatusFailed && state.Original.Status != StatusUnknown {
			return NewMessage{}, ErrNotSendableAgain
		}
		if err := s.checkSender(userID, state.Sender); err != nil {
			return NewMessage{}, err
		}
		// The account's address now, which a send that failed
		// sender-changed has saved.
		return newMessage(state.Sender, state.Attempts, attempt.At, state.Original.Content)
	})
}

// Get returns userID's message messageID, or [ErrNotFound] when userID has
// no such message.
func (s *Service) Get(ctx context.Context, userID int64, messageID uuid.UUID) (Message, error) {
	return s.storage.ReadOutgoingMessage(ctx, userID, messageID)
}

// List returns userID's newest 50 messages, newest first, each with an empty
// [Content.Body].
func (s *Service) List(ctx context.Context, userID int64) ([]Message, error) {
	return s.storage.ListOutgoingMessages(ctx, userID, maxListed)
}

// attempt returns userID's attempt to send now.
func (s *Service) attempt(userID int64) Attempt {
	now := s.now().UTC()
	return Attempt{UserID: userID, At: now, Since: now.Add(-longSendLimit.Window)}
}

// checkSender returns [ErrNeedsReconnect] when Google refused userID's
// sender's token, [ErrTokenUnreadable] when no key opens it, and nil when a
// send from it can go out.
//
// DESIGN: both refusals come before the send limit: a send would only fail,
// so the member hears it now and reconnects, and the attempt is not
// counted.
func (s *Service) checkSender(userID int64, sender Sender) error {
	if sender.NeedsReconnect {
		return ErrNeedsReconnect
	}
	if err := s.tokens.CheckToken(userID, sender.ConnectedAccountID, sender.Credential); err != nil {
		if errors.Is(err, connectedaccount.ErrTokenUnreadable) {
			return ErrTokenUnreadable
		}
		return err
	}
	return nil
}

// newMessage returns content as a message to save from sender, or an
// [*attemptlimit.Error] wrapping [ErrThrottled] when the member's attempts
// leave no room at now.
func newMessage(sender Sender, attempts []time.Time, now time.Time, content Content) (NewMessage, error) {
	if wait := attemptlimit.Wait(attempts, now, shortSendLimit, longSendLimit); wait > 0 {
		return NewMessage{}, &attemptlimit.Error{Err: ErrThrottled, Wait: wait}
	}
	messageID, err := newMessageID(sender.Email)
	if err != nil {
		return NewMessage{}, err
	}
	return NewMessage{From: sender.Email, Content: content, InbokMessageID: messageID}, nil
}

// sameContent reports whether a and b are the same recipients, in order,
// subject, and body.
func sameContent(a, b Content) bool {
	return slices.Equal(a.To, b.To) && slices.Equal(a.Cc, b.Cc) && slices.Equal(a.Bcc, b.Bcc) &&
		a.Subject == b.Subject && a.Body == b.Body
}
