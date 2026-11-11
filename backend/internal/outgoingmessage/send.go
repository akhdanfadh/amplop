package outgoingmessage

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"inbok/internal/connectedaccount"
	"inbok/internal/emailaddress"
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
	// or no part of the request can have reached it. It ends by its own
	// deadline, since [Sending.Send] calls it with a context no stop cancels.
	Send(ctx context.Context, token connectedaccount.AccessToken, raw []byte) (Sent, error)

	// FindSent returns the message Gmail added to Sent after historyID
	// whose X-Inbok-Message-Id is inbokMessageID. It returns
	// [ErrSentNotFound] when it read every such message and none matched,
	// and [ErrCheckIncomplete] when it could not read them all.
	FindSent(ctx context.Context, token connectedaccount.AccessToken, historyID, inbokMessageID string) (Sent, error)
}

// tokens hands out access tokens for connected accounts, e.g.
// [connectedaccount.Access].
type tokens interface {
	Token(ctx context.Context, accountID int64) (connectedaccount.AccessToken, error)
}

// Transition is a message's next state, which [Sending] decides and
// storage saves.
type Transition struct {
	Status Status
	// Failure is set only when [Transition.Status] is [StatusFailed].
	Failure Failure
	// Sent and SentAt are set only when [Transition.Status] is [StatusSent].
	Sent   Sent
	SentAt *time.Time
	// SenderEmail, when not empty, is the sending account's address now,
	// saved on the account.
	SenderEmail string
}

// SendState is a message as storage reads it under the message's lock,
// with its sender's state.
type SendState struct {
	Message
	// SenderDeactivated is whether the sending account's member is
	// deactivated, read under the member's share lock, as ADR029 asks
	// before provider work.
	SenderDeactivated bool
}

// SendStorage keeps what sending reads and changes.
type SendStorage interface {
	// ReadOutgoingMessageToSend returns message messageID, or [ErrNotFound]
	// when there is none, e.g. after its account was disconnected.
	ReadOutgoingMessageToSend(ctx context.Context, messageID uuid.UUID) (Message, error)

	// UpdateOutgoingMessage reads message messageID's sending member under
	// the member's share lock, then locks the message's account, then the
	// message, calls update with both, and saves the [Transition] update
	// returns, with its sender's address on the account when it names one,
	// in one transaction. The account comes before the message because a
	// disconnect locks them in that order. It returns the saved message, or
	// [ErrNotFound] when there is no such message. An error from update
	// rolls back and is returned unchanged.
	UpdateOutgoingMessage(ctx context.Context, messageID uuid.UUID, update func(SendState) (Transition, error)) (Message, error)
}

// Preparation is what [Sending.Prepare] found.
type Preparation struct {
	// Status is the message's status: [StatusWaiting] when sending goes on,
	// anything else when the message has ended, or empty when it is gone.
	Status Status
	// HistoryID is Gmail's position in the mailbox before the send.
	HistoryID string
}

// Sending sends saved messages, one step at a time, for a job that runs the
// steps in order and repeats one that returns an error: [Sending.Prepare],
// then [Sending.Send], and after an unanswered send, [Sending.Check]; and
// [Sending.GiveUp] when a step keeps failing.
//
// DESIGN: the message's status is the record of where sending got, so each
// step reads it first and a step run again after a crash does what is left.
// [StatusSending] means a request may have reached Gmail: a send is never
// repeated after it, only checked (ADR006, Sending). What a step that sends
// relies on, the sender's address and an active member, is checked in that
// step, since DBOS reuses an earlier step's result after a restart however
// long ago it ran.
type Sending struct {
	storage  SendStorage
	tokens   tokens
	provider Provider
	now      func() time.Time
}

// NewSending supplies storage, access tokens, Gmail, and a clock.
func NewSending(storage SendStorage, tokens tokens, provider Provider, now func() time.Time) *Sending {
	return &Sending{storage: storage, tokens: tokens, provider: provider, now: now}
}

// Prepare reads message messageID and, while it waits, notes Gmail's
// position in the mailbox, from which [Sending.Check] looks. It ends the
// message failed when the account cannot send. It returns the message's
// status and the position, or an error that may pass.
func (s *Sending) Prepare(ctx context.Context, messageID uuid.UUID) (Preparation, error) {
	// 1. Stop for a message that ended or went.
	message, err := s.storage.ReadOutgoingMessageToSend(ctx, messageID)
	if errors.Is(err, ErrNotFound) {
		return Preparation{}, nil
	}
	if err != nil {
		return Preparation{}, err
	}
	if message.Status != StatusWaiting {
		return Preparation{Status: message.Status}, nil
	}

	// 2. Read the mailbox, with a token whose stop errors end the message.
	token, err := s.tokens.Token(ctx, message.ConnectedAccountID)
	if status, stopped, err := s.stopped(ctx, messageID, err, StatusFailed); stopped {
		return Preparation{Status: status}, err
	}
	mailbox, err := s.provider.Mailbox(ctx, token)
	if err != nil {
		return Preparation{}, fmt.Errorf("read mailbox: %w", err)
	}
	return Preparation{Status: StatusWaiting, HistoryID: mailbox.HistoryID}, nil
}

// Send sends message messageID once, if it waits, after checking that Gmail
// names the account by the message's From. It ends the message failed when the
// account cannot send, or when the address differs, saving Gmail's on the
// account. It returns the message's status: [StatusSending] when the send went
// unanswered, so only [Sending.Check] can tell whether it was sent, and empty
// when the message is gone. It returns an error, with the message waiting
// again, when the message was not sent and trying again may work.
func (s *Sending) Send(ctx context.Context, messageID uuid.UUID) (Status, error) {
	// 1. Stop for a message that ended or went, or whose send may have
	// reached Gmail before a crash.
	message, err := s.storage.ReadOutgoingMessageToSend(ctx, messageID)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if message.Status != StatusWaiting {
		return message.Status, nil
	}

	// 2. Build the message and take a token before marking it sending, so a
	// failure here leaves it waiting.
	raw, err := compose(message)
	if err != nil {
		return "", fmt.Errorf("compose message: %w", err)
	}
	token, err := s.tokens.Token(ctx, message.ConnectedAccountID)
	if status, stopped, err := s.stopped(ctx, messageID, err, StatusFailed); stopped {
		return status, err
	}

	// 3. Refuse to send from another address than the member saw: Gmail
	// would send from the account's address, or an alias, without telling.
	// Saving Gmail's address shows the member the new one next time.
	mailbox, err := s.provider.Mailbox(ctx, token)
	if err != nil {
		return "", fmt.Errorf("read mailbox: %w", err)
	}
	current, err := emailaddress.Parse(mailbox.Email)
	if err != nil {
		return "", fmt.Errorf("gmail's address for the account: %w", err)
	}
	if from, err := emailaddress.Parse(message.From); err != nil || from != current {
		return s.end(ctx, messageID, Transition{Status: StatusFailed, Failure: FailureSenderChanged, SenderEmail: current})
	}

	// 4. Mark it sending before the request, so a crash during it is
	// checked, never sent again. The member's deactivation is read again
	// here, under their share lock, so one that committed since the
	// token's last read stops the send (ADR029).
	marked := false
	latest, err := s.storage.UpdateOutgoingMessage(ctx, messageID, func(state SendState) (Transition, error) {
		marked = false
		switch {
		case state.Status != StatusWaiting:
			return unchanged(state.Message), nil
		case state.SenderDeactivated:
			return Transition{Status: StatusFailed, Failure: FailureDeactivated}, nil
		}
		marked = true
		return Transition{Status: StatusSending}, nil
	})
	if err != nil || !marked {
		return statusOf(latest, err), ignoreGone(err)
	}

	// 5. Send, and save what Gmail answered. From here a stop, e.g. the
	// worker shutting down, cancels neither: cutting the request off would
	// leave the message sending, and the check would end a send Gmail
	// answered, or one that never left, unknown. The request keeps the
	// provider's own deadline; a worker killed meanwhile resumes the step,
	// which finds the message sending and checks it.
	ctx = context.WithoutCancel(ctx)
	sent, err := s.provider.Send(ctx, token, raw)
	switch {
	case err == nil:
		at := s.now().UTC()
		return s.end(ctx, messageID, Transition{Status: StatusSent, Sent: sent, SentAt: &at})
	case errors.Is(err, ErrRefused):
		return s.end(ctx, messageID, Transition{Status: StatusFailed, Failure: FailureRefused})
	case errors.Is(err, ErrSendingLimit):
		return s.end(ctx, messageID, Transition{Status: StatusFailed, Failure: FailureSendingLimit})
	case errors.Is(err, ErrNotSent):
		status, saveErr := s.end(ctx, messageID, Transition{Status: StatusWaiting})
		return status, errors.Join(err, saveErr)
	default:
		// Unanswered: the message stays sending for the check.
		return StatusSending, nil
	}
}

// Check looks in Sent for message messageID after an unanswered send, from
// historyID, Gmail's position before it. It ends the message sent when
// Gmail has it, and unknown when the check finds nothing or cannot tell. It
// returns the message's status, empty when the message is gone, or an error
// that may pass.
func (s *Sending) Check(ctx context.Context, messageID uuid.UUID, historyID string) (Status, error) {
	// 1. Stop for a message that ended or went.
	message, err := s.storage.ReadOutgoingMessageToSend(ctx, messageID)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if message.Status != StatusSending {
		return message.Status, nil
	}

	// 2. Look for it. Without a token the check cannot tell.
	token, err := s.tokens.Token(ctx, message.ConnectedAccountID)
	if status, stopped, err := s.stopped(ctx, messageID, err, StatusUnknown); stopped {
		return status, err
	}
	sent, err := s.provider.FindSent(ctx, token, historyID, message.InbokMessageID)
	switch {
	case err == nil:
		at := s.now().UTC()
		return s.end(ctx, messageID, Transition{Status: StatusSent, Sent: sent, SentAt: &at})
	case errors.Is(err, ErrSentNotFound), errors.Is(err, ErrCheckIncomplete):
		// Not finding it does not prove Gmail never took it (ADR006).
		return s.end(ctx, messageID, Transition{Status: StatusUnknown})
	default:
		return "", fmt.Errorf("find sent message: %w", err)
	}
}

// GiveUp ends message messageID after a step kept failing: failed when it
// waits, so it was not sent, and unknown when it is sending, so it may have
// been. It returns the message's status, empty when it is gone.
func (s *Sending) GiveUp(ctx context.Context, messageID uuid.UUID) (Status, error) {
	ended, err := s.storage.UpdateOutgoingMessage(ctx, messageID, func(state SendState) (Transition, error) {
		switch state.Status {
		case StatusWaiting:
			return Transition{Status: StatusFailed, Failure: FailureGaveUp}, nil
		case StatusSending:
			return Transition{Status: StatusUnknown}, nil
		}
		return unchanged(state.Message), nil
	})
	return statusOf(ended, err), ignoreGone(err)
}

// stopped handles err, from [connectedaccount.Access.Token] for message
// messageID's account. When err stops all work for the account, it ends
// the message as status, failed with the reason or unknown, and returns its
// status and true; when the account, and so the message, is gone, it
// returns empty and true. It returns false for no error, and wraps any
// other error, which may pass.
func (s *Sending) stopped(ctx context.Context, messageID uuid.UUID, err error, status Status) (Status, bool, error) {
	if err == nil {
		return "", false, nil
	}
	if errors.Is(err, connectedaccount.ErrNotFound) {
		return "", true, nil
	}
	failure, ok := stopFailure(err)
	if !ok {
		return "", true, fmt.Errorf("access token: %w", err)
	}
	next := Transition{Status: status}
	if status == StatusFailed {
		next.Failure = failure
	}
	ended, err := s.end(ctx, messageID, next)
	return ended, true, err
}

// end saves next for message messageID unless it already ended, and returns
// its status, empty when it is gone.
func (s *Sending) end(ctx context.Context, messageID uuid.UUID, next Transition) (Status, error) {
	ended, err := s.storage.UpdateOutgoingMessage(ctx, messageID, func(state SendState) (Transition, error) {
		if state.Status != StatusWaiting && state.Status != StatusSending {
			return unchanged(state.Message), nil
		}
		return next, nil
	})
	return statusOf(ended, err), ignoreGone(err)
}

// stopFailure returns the failure for err, an error of
// [connectedaccount.Access.Token] that stops all work for the account, and
// whether it is one.
func stopFailure(err error) (Failure, bool) {
	switch {
	case errors.Is(err, connectedaccount.ErrNeedsReconnect):
		return FailureNeedsReconnect, true
	case errors.Is(err, connectedaccount.ErrTokenUnreadable):
		return FailureTokenUnreadable, true
	case errors.Is(err, connectedaccount.ErrDeactivated):
		return FailureDeactivated, true
	}
	return "", false
}

// unchanged returns message's state as a [Transition] that saves nothing new.
func unchanged(message Message) Transition {
	return Transition{
		Status:  message.Status,
		Failure: message.Failure,
		Sent:    Sent{ProviderMessageID: message.ProviderMessageID, ProviderThreadID: message.ProviderThreadID},
		SentAt:  message.SentAt,
	}
}

// statusOf returns message's status, or empty when err says it is gone.
func statusOf(message Message, err error) Status {
	if err != nil {
		return ""
	}
	return message.Status
}

// ignoreGone returns err, or nil when it says the message is gone.
func ignoreGone(err error) error {
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
