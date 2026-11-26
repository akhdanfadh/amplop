package messagecontent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"amplop/internal/connectedaccount"
	"amplop/internal/mailsync"
)

// Errors a [Provider] returns, which end a fetch for good: fetching again
// soon gives the same answer.
var (
	// ErrGone means the provider no longer has the message, or the message
	// no such part.
	ErrGone = errors.New("messagecontent: the provider no longer has it")
	// ErrTooLarge means the provider's answer passed amplop's cap.
	ErrTooLarge = errors.New("messagecontent: the provider's answer is too large")
	// ErrUnreadable means the provider refused the request for this
	// message, or its answer could not be read, e.g. content that is not
	// the encoding it should be.
	ErrUnreadable = errors.New("messagecontent: the provider refused the message or its answer cannot be read")
)

// ErrPartNotFound means a message has no saved part at a path, or only a
// multipart there, which has no content of its own.
var ErrPartNotFound = errors.New("messagecontent: message part not found")

const (
	// bodyBatch is how many messages one [Fetcher.FetchDue] fetches, amplop's
	// choice: at Gmail's 80 a minute, a step of about 15 seconds.
	bodyBatch = 20
	// retryFailedAfter is how long the background fetch skips a message
	// whose fetch failed for good, amplop's choice: such a failure seldom
	// passes, so the minute's fetch does not ask again each time, while a
	// provider that changes its mind is asked again the next day.
	retryFailedAfter = 24 * time.Hour
)

// Provider fetches a message's content from its provider, with an access
// token for the account. Each method only reads, so a retried call
// changes nothing. Each returns [ErrGone] when the provider no longer has
// the message, [ErrTooLarge] when its answer passes amplop's cap, and
// [ErrUnreadable] when it refuses the request for this message or its
// answer cannot be read. Any other error, e.g. the provider's rate limits
// or a network failure, may pass by itself.
type Provider interface {
	// Message returns account accountID's message providerID whole, as
	// [Parse] reads it, keeping the account's fetches, across calls, to
	// the provider's limits for one account.
	Message(ctx context.Context, token connectedaccount.AccessToken, accountID int64, providerID string) (Received, error)
	// PartContent returns the content of message providerID's part
	// partID, a [Part.ID], transfer encoding undone, and the charset the
	// provider converted it to, as [Received.ContentCharset], keeping the
	// account's fetches to its limits as [Provider.Message] does. It
	// returns [ErrGone] also when the message has no such part.
	PartContent(ctx context.Context, token connectedaccount.AccessToken, accountID int64, providerID, partID string) ([]byte, string, error)
}

// accountAccess hands out access tokens, e.g. [connectedaccount.Access].
type accountAccess interface {
	Token(ctx context.Context, accountID int64) (connectedaccount.AccessToken, error)
}

// PendingBody is a message whose body the background fetch takes.
type PendingBody struct {
	ID         uuid.UUID
	ProviderID string
}

// DueFilter is which messages without parts the background fetch takes:
// amplop's policy, which storage applies.
type DueFilter struct {
	// Since leaves out messages received before it, by the provider's own
	// timestamp.
	Since time.Time
	// FailedSince leaves out messages whose fetch failed for good at or
	// after it.
	FailedSince time.Time
	// ExcludedRoles leaves out messages in a mailbox with any of these
	// roles.
	ExcludedRoles []mailsync.Role
}

// FetchStorage keeps what a [Fetcher] reads about messages and records
// of its fetches.
type FetchStorage interface {
	// AccountsWithDueBodies returns, in ID order, the accounts whose member
	// is active, which need no reconnect, whose full sync is complete, and
	// for which [FetchStorage.MessagesNeedingBodies] lists at least one
	// message for filter.
	AccountsWithDueBodies(ctx context.Context, filter DueFilter) ([]int64, error)
	// MessagesNeedingBodies returns up to limit of account accountID's
	// messages without parts that filter keeps, newest first.
	MessagesNeedingBodies(ctx context.Context, accountID int64, filter DueFilter, limit int) ([]PendingBody, error)
	// MarkBodyFailed records at as when fetching account accountID's
	// message messageID's body last failed for good. It returns
	// [ErrMessageNotFound] when the account has no such message. In one
	// transaction, it 1. locks the account's sync state, as every write to
	// the account's mail does first; 2. records the time.
	MarkBodyFailed(ctx context.Context, accountID int64, messageID uuid.UUID, at time.Time) error
	// MessageProviderID returns the provider's ID of account accountID's
	// message messageID, or [ErrMessageNotFound] when there is no such
	// message.
	MessageProviderID(ctx context.Context, accountID int64, messageID uuid.UUID) (string, error)
	// MessageParts returns the part tree of account accountID's message
	// messageID, and false when it has none, or no such message.
	MessageParts(ctx context.Context, accountID int64, messageID uuid.UUID) (Part, bool, error)
}

// Fetcher fetches messages' content from their provider and keeps it in
// [Contents]: in the background, the bodies of the recent messages a
// member would read next, and on request, a message or part a member
// opens.
type Fetcher struct {
	contents *Contents
	storage  FetchStorage
	provider Provider
	access   accountAccess
	now      func() time.Time
	logger   *slog.Logger
}

// NewFetcher returns a [Fetcher] keeping content in contents, given
// storage, the provider it fetches from, access to its accounts' tokens,
// a clock, and a logger for the messages whose fetch fails for good.
func NewFetcher(contents *Contents, storage FetchStorage, provider Provider, access accountAccess, now func() time.Time, logger *slog.Logger) *Fetcher {
	return &Fetcher{contents: contents, storage: storage, provider: provider, access: access, now: now, logger: logger}
}

// Batch is what one [Fetcher.FetchDue] did.
type Batch struct {
	Saved  int  // messages whose content it saved
	Failed int  // messages whose fetch failed for good
	More   bool // whether more messages may be due
}

// DueAccounts returns the accounts whose messages' bodies the background
// fetch takes: those whose member is active, which need no reconnect,
// whose full sync is complete, and which have a message received in the
// last 90 days without parts, in no junk or trash mailbox, whose fetch
// has not failed in the last day.
//
// DESIGN: Bodies wait for the full sync, which copies metadata at the
// whole of the account's provider pace: a member waits for the list, while
// a body is fetched when they open its message anyway. The accounts are
// found from their data each time, not from events, so a run that ended
// at its limit or failed, or a failure's day that passed, is picked up the
// next time without anything having to remember it.
func (f *Fetcher) DueAccounts(ctx context.Context) ([]int64, error) {
	accounts, err := f.storage.AccountsWithDueBodies(ctx, f.dueFilter())
	if err != nil {
		return nil, fmt.Errorf("list accounts with due bodies: %w", err)
	}
	return accounts, nil
}

// FetchDue fetches and saves the content of up to 20 of account
// accountID's messages whose bodies are due, as [Fetcher.DueAccounts]
// counts them, newest first, one at a time. A message whose fetch fails for good is recorded and skipped for a
// day. It returns what it did, the access error that stops the account's
// provider work, or the first other error, which ends the batch: the next
// one fetches the rest.
func (f *Fetcher) FetchDue(ctx context.Context, accountID int64) (Batch, error) {
	// 1. Take an access token, which stops a stopped account before any
	// provider call.
	token, err := f.access.Token(ctx, accountID)
	if err != nil {
		return Batch{}, err
	}

	// 2. Take the newest messages due. Asked again each batch, so mail that
	// arrives during a long fetch goes first.
	pending, err := f.storage.MessagesNeedingBodies(ctx, accountID, f.dueFilter(), bodyBatch)
	if err != nil {
		return Batch{}, fmt.Errorf("list messages needing bodies: %w", err)
	}

	// 3. Fetch each in turn: one fetch at a time keeps the account's pace
	// free for a sync's fetches meanwhile.
	batch := Batch{More: len(pending) == bodyBatch}
	for _, message := range pending {
		err := f.fetch(ctx, token, accountID, message.ID, message.ProviderID)
		switch {
		case err == nil:
			batch.Saved++
		// Synchronization deleted it meanwhile.
		case errors.Is(err, ErrMessageNotFound):
		case failsForGood(err):
			f.logger.WarnContext(ctx, "message body fetch failed", "account_id", accountID, "message_id", message.ID, "err", err)
			if err := f.storage.MarkBodyFailed(ctx, accountID, message.ID, f.now().UTC()); err != nil && !errors.Is(err, ErrMessageNotFound) {
				return batch, fmt.Errorf("mark body failed: %w", err)
			}
			batch.Failed++
		default:
			return batch, err
		}
	}
	return batch, nil
}

// dueFilter returns the background fetch's policy as of now: the last 90
// days, failures skipped for a day, and no spam or trash.
func (f *Fetcher) dueFilter() DueFilter {
	now := f.now().UTC()
	return DueFilter{
		Since:         now.Add(-mailsync.Window),
		FailedSince:   now.Add(-retryFailedAfter),
		ExcludedRoles: mailsync.MetadataOnlyRoles(),
	}
}

// failsForGood reports whether err, from fetching one message, fails it
// whenever it is fetched soon again.
func failsForGood(err error) bool {
	return errors.Is(err, ErrGone) || errors.Is(err, ErrTooLarge) || errors.Is(err, ErrUnreadable) ||
		errors.Is(err, ErrTooManyParts)
}

// FetchMessage fetches account accountID's message messageID's content
// from its provider and saves it, whatever its date, mailbox, or earlier
// failure, as when a member opens it. A message whose parts are saved
// keeps them, and its text is stored again under the IDs they name, which
// restores text the store lost. It returns [ErrMessageNotFound], an
// access error from [connectedaccount.Access.Token], a [Provider]'s
// errors, [ErrTooManyParts], or a store's or storage's error.
func (f *Fetcher) FetchMessage(ctx context.Context, accountID int64, messageID uuid.UUID) error {
	providerID, err := f.storage.MessageProviderID(ctx, accountID, messageID)
	if err != nil {
		return err
	}
	token, err := f.access.Token(ctx, accountID)
	if err != nil {
		return err
	}
	return f.fetch(ctx, token, accountID, messageID, providerID)
}

// fetch fetches account accountID's message messageID, providerID at its
// provider, with token, and saves its parts and text.
func (f *Fetcher) fetch(ctx context.Context, token connectedaccount.AccessToken, accountID int64, messageID uuid.UUID, providerID string) error {
	received, err := f.provider.Message(ctx, token, accountID, providerID)
	if err != nil {
		return fmt.Errorf("fetch message: %w", err)
	}
	root, texts, err := Parse(received)
	if err != nil {
		return err
	}
	// A provider may send a long text part separately, e.g. Gmail by
	// attachmentId; without it, the message would show no text.
	for _, part := range separateTexts(root, texts) {
		content, contentCharset, err := f.provider.PartContent(ctx, token, accountID, providerID, part.ID)
		if err != nil {
			return fmt.Errorf("fetch part %q: %w", part.ID, err)
		}
		texts = append(texts, Text{PartID: part.ID, Content: DecodeFrom(part, contentCharset, content)})
	}
	return f.contents.Save(ctx, accountID, messageID, root, texts)
}

// separateTexts returns the text parts of root that a view may show, as
// [Parse] collects them, that have no [Text] in texts: those whose
// content the provider sends separately.
func separateTexts(root Part, texts []Text) []Part {
	have := make(map[string]bool, len(texts))
	for _, text := range texts {
		have[text.PartID] = true
	}
	var missing []Part
	var walk func(part Part)
	walk = func(part Part) {
		if part.Kind == KindText && !have[part.ID] {
			missing = append(missing, part)
		}
		// Only a multipart's children are shown, as in parse.
		if part.Kind != KindMultipart {
			return
		}
		for _, child := range part.Parts {
			walk(child)
		}
	}
	walk(root)
	return missing
}

// FetchPart fetches the content of the part at path of account
// accountID's message messageID from its provider, stores it, and returns
// the part naming it: a text part's content converted to UTF-8 as
// [DecodeFrom] does, any other's as the provider gives it. A part stored before is
// stored again under its ID, which restores content the store lost. The
// message's parts must be saved, e.g. by [Fetcher.FetchMessage]. It
// returns [ErrMessageNotFound], [ErrPartNotFound] when the message has no
// saved part there or only a multipart, an access error, a [Provider]'s
// errors, or a store's or storage's error.
func (f *Fetcher) FetchPart(ctx context.Context, accountID int64, messageID uuid.UUID, path []int) (Part, error) {
	providerID, err := f.storage.MessageProviderID(ctx, accountID, messageID)
	if err != nil {
		return Part{}, err
	}
	root, ok, err := f.storage.MessageParts(ctx, accountID, messageID)
	if err != nil {
		return Part{}, fmt.Errorf("read message parts: %w", err)
	}
	part, found := partAt(root, path)
	if !ok || !found || part.Kind == KindMultipart {
		return Part{}, ErrPartNotFound
	}

	token, err := f.access.Token(ctx, accountID)
	if err != nil {
		return Part{}, err
	}
	content, contentCharset, err := f.provider.PartContent(ctx, token, accountID, providerID, part.ID)
	if err != nil {
		return Part{}, fmt.Errorf("fetch part: %w", err)
	}
	// A file is kept as received; a provider may have converted a text
	// part's bytes, e.g. Gmail's inline data, which then stays converted.
	if part.Kind == KindText {
		content = DecodeFrom(part, contentCharset, content)
	}
	return f.contents.SavePart(ctx, accountID, messageID, path, part, content)
}

// partAt returns the part of root at path, the index of each part on the
// way down, and whether there is one.
func partAt(root Part, path []int) (Part, bool) {
	part := root
	for _, i := range path {
		if i < 0 || i >= len(part.Parts) {
			return Part{}, false
		}
		part = part.Parts[i]
	}
	return part, true
}
