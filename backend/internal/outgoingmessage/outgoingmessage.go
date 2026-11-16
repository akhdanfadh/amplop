// Package outgoingmessage owns the messages members send from their
// connected accounts (US015), new ones and replies (US009). [Service] checks
// a member's draft, gives it a Message-ID, and saves it for the job that
// sends it, sends a failed message again as a copy, and reads back a
// member's own messages with their status.
package outgoingmessage

import (
	"context"
	"errors"
	"fmt"
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
	// ErrAnsweredNotFound means the member has no synchronized message with
	// the ID a reply answers, including one another member's account holds,
	// or one deleted since.
	ErrAnsweredNotFound = errors.New("answered message not found")
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
	// of the member's messages: other content, another sending account, or
	// another answered message.
	ErrKeyReused = errors.New("idempotency key already used for another message")
	// ErrNotSendableAgain means the message is waiting, sending, or sent, so
	// sending it again could send it twice.
	ErrNotSendableAgain = errors.New("only a failed or unknown message can be sent again")
)

// Action is what a member can ask of one of their messages.
type Action string

// ActionSendAgain is [Service.SendAgain].
const ActionSendAgain Action = "send-again"

// Actions are every [Action], in the order the API lists them.
var Actions = []Action{ActionSendAgain}

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
	Threading
	Status Status
	// Failure is set only when [Message.Status] is [StatusFailed].
	Failure Failure
	// ProviderMessageID is Gmail's ID for the message, set once it is sent.
	ProviderMessageID string
	// ProviderThreadID is Gmail's ID for the message's thread: for a reply
	// from the answered message's account, the thread it is sent into, set
	// when saved; for any other message, set once it is sent.
	ProviderThreadID string
	CreatedAt        time.Time
	// SentAt is when Gmail accepted the message; nil until then.
	SentAt *time.Time
	// SentAgainAs is the copy the member sent again of this failed or
	// unknown message; nil until then.
	SentAgainAs *uuid.UUID
	// Account is the sending account as [Service] read it with the message,
	// never saved with it; the zero value once a disconnect deleted it.
	Account connectedaccount.Account
}

// AllowedActions returns the actions accepted for m now; offered says
// whether its provider's client is set up, which every send needs. Sending
// it again needs it failed or unknown, and its account's
// [connectedaccount.ActionSend], unless it was already sent again: that
// returns the same copy before the account is read.
//
// DESIGN: the member's send limit and deactivation are left to the
// refusal: both are the member's, not the message's, and the limit lifts
// with time, which the refusal's wait says.
func (m Message) AllowedActions(offered bool) []Action {
	if !offered || !sendableAgain(m.Status) {
		return nil
	}
	if m.SentAgainAs == nil && !slices.Contains(m.Account.AllowedActions(offered), connectedaccount.ActionSend) {
		return nil
	}
	return []Action{ActionSendAgain}
}

// sendableAgain reports whether a message in status can be sent again:
// waiting or sending would make two sends of one message, and a sent
// message reached its recipients.
func sendableAgain(status Status) bool {
	return status == StatusFailed || status == StatusUnknown
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

// Threading is what threads a reply with the message it answers: its
// In-Reply-To and References headers (RFC 5322 section 3.6.4), each msg-id
// with its angle brackets and [msgid.Writable]. A new message has neither.
type Threading struct {
	// InReplyTo is the answered message's Message-ID, or empty when it has
	// none that can be written.
	InReplyTo string
	// References is the thread's msg-ids, oldest first, at most
	// [msgid.MaxReferences]; empty, never nil, when there are none.
	References []string
}

// NewMessage is a message to save, before it has an ID.
type NewMessage struct {
	// From is the sending account's address.
	From string
	Content
	InbokMessageID string
	Threading
	// ProviderThreadID is the Gmail thread a reply is sent into, or empty.
	ProviderThreadID string
}

// Answered is the synchronized message a reply answers, as storage reads it.
type Answered struct {
	// ConnectedAccountID is the account that holds the message.
	ConnectedAccountID int64
	// ProviderThreadID is Gmail's ID for its thread in that account.
	ProviderThreadID string
	Subject          string
	// InternetMessageID, InReplyTo, and References are its own threading
	// headers as synchronized, each msg-id with its angle brackets;
	// InternetMessageID is empty when it has none.
	InternetMessageID string
	InReplyTo         []string
	References        []string
}

// Request is what a request to send saves a message under: the sending
// account, the request's idempotency key, and for a reply, the answered
// message. A repeat with the key is the same request only when it names the
// same account and answered message.
type Request struct {
	AccountID int64
	Key       uuid.UUID
	// AnsweredID is the member's synchronized message a reply answers; nil
	// for a new message.
	AnsweredID *uuid.UUID
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
	// Answered is the message a reply answers; nil for a new message.
	Answered *Answered
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
	//  2. returns [ErrKeyExists] when the member saved a message with
	//     request.Key;
	//  3. reads the member's account request.AccountID, keeping it from
	//     being disconnected until the transaction ends, or returns
	//     [ErrAccountNotFound];
	//  4. reads the member's synchronized message request.AnsweredID, when
	//     set, or returns [ErrAnsweredNotFound], and calls build with it, the
	//     account, and the member's attempts after attempt.Since;
	//  5. saves the message build returns as waiting, with the job that sends
	//     it, so no message waits without a job, an attempt at attempt.At,
	//     deleting the member's attempts at or before attempt.Since, and
	//     request, returning [ErrKeyExists] if another transaction saved its
	//     key first.
	//
	// It returns the saved message. An error from build rolls back and is
	// returned unchanged.
	InsertOutgoingMessage(ctx context.Context, attempt Attempt, request Request, build func(CreateState) (NewMessage, error)) (Message, error)

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

	// ReadOutgoingMessageByKey returns userID's message saved with key and
	// the request that saved it, or [ErrNotFound] when userID has none.
	ReadOutgoingMessageByKey(ctx context.Context, userID int64, key uuid.UUID) (Message, Request, error)

	// ListOutgoingMessages returns userID's newest messages, at most limit,
	// newest first, each with an empty [Content.Body].
	ListOutgoingMessages(ctx context.Context, userID int64, limit int) ([]Message, error)
}

// sendingAccounts reads the accounts messages are sent from;
// [connectedaccount.Service] does.
type sendingAccounts interface {
	// CheckToken returns an error wrapping
	// [connectedaccount.ErrTokenUnreadable] when no key opens credential,
	// the token of userID's account accountID.
	CheckToken(userID, accountID int64, credential connectedaccount.Credential) error
	// List returns userID's accounts, each with its status now.
	List(ctx context.Context, userID int64) ([]connectedaccount.Account, error)
}

// Service applies the rules for outgoing messages.
type Service struct {
	storage  Storage
	accounts sendingAccounts
	now      func() time.Time
}

// NewService supplies storage, what reads the sending accounts, and the
// clock the send limit counts by.
func NewService(storage Storage, accounts sendingAccounts, now func() time.Time) *Service {
	return &Service{storage: storage, accounts: accounts, now: now}
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
	request := Request{AccountID: draft.ConnectedAccountID, Key: draft.IdempotencyKey}
	message, err := s.create(ctx, userID, request, draft.Content)
	if err != nil {
		return Message{}, err
	}
	return s.withAccount(ctx, userID, message)
}

// create saves content as userID's message for request, as [Service.Create]
// and [Service.Reply] describe.
func (s *Service) create(ctx context.Context, userID int64, request Request, content Content) (Message, error) {
	// 1. Refuse content that cannot be sent before any storage.
	content, err := parseContent(content)
	if err != nil {
		return Message{}, err
	}

	attempt := s.attempt(userID)
	for tries := 1; ; tries++ {
		// 2. With the member and account held, refuse what would only fail
		// or go over the limit, and save.
		message, err := s.storage.InsertOutgoingMessage(ctx, attempt, request, func(state CreateState) (NewMessage, error) {
			if err := s.checkSender(userID, state.Sender); err != nil {
				return NewMessage{}, err
			}
			next, err := newMessage(state.Sender, state.Attempts, attempt.At, content)
			if err != nil || state.Answered == nil {
				return next, err
			}
			return replyTo(next, state.Sender, *state.Answered), nil
		})
		if !errors.Is(err, ErrKeyExists) {
			return message, err
		}

		// 3. A repeated request, e.g. one whose answer was lost, gets the
		// message the first saved; the key's check comes before the limit's,
		// so a repeat is never refused for the attempt it repeats.
		message, err = s.repeated(ctx, userID, request, content)
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

// repeated returns userID's message saved with request's key when the
// request that saved it is request with content, [ErrKeyReused], or
// errKeyFreed when the message is gone.
func (s *Service) repeated(ctx context.Context, userID int64, request Request, content Content) (Message, error) {
	earlier, saved, err := s.storage.ReadOutgoingMessageByKey(ctx, userID, request.Key)
	if errors.Is(err, ErrNotFound) {
		return Message{}, errKeyFreed
	}
	if err != nil {
		return Message{}, err
	}
	// A reply's subject is the answered message's, not the request's.
	if request.AnsweredID != nil {
		content.Subject = earlier.Subject
	}
	// The IETF Idempotency-Key draft refuses a key reused with another
	// payload (draft-ietf-httpapi-idempotency-key-header, section 2.2).
	if saved.AccountID != request.AccountID || !sameAnswered(saved.AnsweredID, request.AnsweredID) || !sameContent(earlier.Content, content) {
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
	copied, err := s.storage.InsertOutgoingMessageCopy(ctx, attempt, messageID, func(state CopyState) (NewMessage, error) {
		if !sendableAgain(state.Original.Status) {
			return NewMessage{}, ErrNotSendableAgain
		}
		if err := s.checkSender(userID, state.Sender); err != nil {
			return NewMessage{}, err
		}
		// The account's address now, which a send that failed
		// sender-changed has saved.
		next, err := newMessage(state.Sender, state.Attempts, attempt.At, state.Original.Content)
		if err != nil {
			return NewMessage{}, err
		}
		// A reply's copy answers the same message, in the same thread: the
		// account is the original's.
		next.Threading = state.Original.Threading
		next.ProviderThreadID = state.Original.ProviderThreadID
		return next, nil
	})
	if err != nil {
		return Message{}, err
	}
	return s.withAccount(ctx, userID, copied)
}

// Get returns userID's message messageID, or [ErrNotFound] when userID has
// no such message.
func (s *Service) Get(ctx context.Context, userID int64, messageID uuid.UUID) (Message, error) {
	message, err := s.storage.ReadOutgoingMessage(ctx, userID, messageID)
	if err != nil {
		return Message{}, err
	}
	return s.withAccount(ctx, userID, message)
}

// List returns userID's newest 50 messages, newest first, each with an empty
// [Content.Body].
func (s *Service) List(ctx context.Context, userID int64) ([]Message, error) {
	messages, err := s.storage.ListOutgoingMessages(ctx, userID, maxListed)
	if err != nil {
		return nil, err
	}
	if err := s.withAccounts(ctx, userID, messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// withAccount returns userID's message with its [Message.Account] read now,
// or the error of reading it.
func (s *Service) withAccount(ctx context.Context, userID int64, message Message) (Message, error) {
	messages := []Message{message}
	if err := s.withAccounts(ctx, userID, messages); err != nil {
		return Message{}, err
	}
	return messages[0], nil
}

// withAccounts sets each of userID's messages' [Message.Account] to the
// account as read now, in one read of userID's accounts, or returns the
// error of that read.
func (s *Service) withAccounts(ctx context.Context, userID int64, messages []Message) error {
	accounts, err := s.accounts.List(ctx, userID)
	if err != nil {
		return fmt.Errorf("read sending accounts: %w", err)
	}
	for i := range messages {
		for _, account := range accounts {
			if account.ID == messages[i].ConnectedAccountID {
				messages[i].Account = account
			}
		}
	}
	return nil
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
	if err := s.accounts.CheckToken(userID, sender.ConnectedAccountID, sender.Credential); err != nil {
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
	return NewMessage{From: sender.Email, Content: content, InbokMessageID: messageID, Threading: Threading{References: []string{}}}, nil
}

// sameAnswered reports whether a and b name the same answered message, or
// both none.
func sameAnswered(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// sameContent reports whether a and b are the same recipients, in order,
// subject, and body.
func sameContent(a, b Content) bool {
	return slices.Equal(a.To, b.To) && slices.Equal(a.Cc, b.Cc) && slices.Equal(a.Bcc, b.Bcc) &&
		a.Subject == b.Subject && a.Body == b.Body
}
