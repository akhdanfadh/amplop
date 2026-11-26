package conversation

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"
	"uuid"

	"golang.org/x/sync/errgroup"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
)

// ========================
// Actions
// ========================

// Action is a change a member makes to conversations, by its name in the
// API, as their provider's own rules apply it, e.g. what Trash does to a
// thread's other labels. A [Change] undoes it.
type Action string

// The actions, each beside its opposite.
const (
	ActionRead      Action = "read"
	ActionUnread    Action = "unread"
	ActionArchive   Action = "archive"   // out of the inbox
	ActionUnarchive Action = "unarchive" // back into the inbox
	ActionTrash     Action = "trash"
	ActionUntrash   Action = "untrash"
	ActionStar      Action = "star"
	ActionUnstar    Action = "unstar"
	ActionSpam      Action = "spam"   // reported as spam, out of the inbox
	ActionUnspam    Action = "unspam" // not spam, back into the inbox
)

// Actions lists every action, each beside its opposite.
var Actions = []Action{
	ActionRead, ActionUnread, ActionArchive, ActionUnarchive,
	ActionTrash, ActionUntrash, ActionStar, ActionUnstar,
	ActionSpam, ActionUnspam,
}

// OnNewest reports whether a changes only a conversation's newest message,
// [Summary.NewestMessageID], rather than every message of its thread.
//
// DESIGN: Star and Mark unread mark one message, as Gmail's own web app
// does: mail clients, Gmail's among them, open a conversation's unread
// messages, so marking each unread would open them all on the next visit,
// and a star on each shows on each. Their opposites clear every message,
// so Unstar and Mark read leave no star or unread message behind, and
// undoing them marks only the newest.
func (a Action) OnNewest() bool {
	return a == ActionStar || a == ActionUnread
}

// MaxActionIDs is how many conversations one action or change reaches at
// most, inbok's choice: half of what actionBudget fits at the slowest rate
// measured, so Gmail at twice as slow still fits. Against a Gmail account at
// actionConcurrency (2026-11-16), Gmail changed about 9 threads a second, 7
// at its slowest (50 in 6.9 s); 100 in one action left 42 unchanged at the
// budget. Each costs a provider call, e.g. a Gmail call of 5, 10, or 20 of
// the member's 6,000 quota units a minute (Gmail's usage limits). A client
// acts on more in requests of this many.
const MaxActionIDs = 25

// Errors of [ActionService.Apply] a caller shows the member.
var (
	// ErrInvalidAction means an action is none of [Actions].
	ErrInvalidAction = errors.New("unknown conversation action")
	// ErrInvalidIDs means an action names no conversation, or more than
	// [MaxActionIDs].
	ErrInvalidIDs = errors.New("conversation count out of range")
)

// Reason is why an action left a conversation as it was.
type Reason string

const (
	// ReasonNotFound means the member has no such conversation, or the
	// provider no longer has its thread; the next sync removes it.
	ReasonNotFound Reason = "not-found"
	// ReasonNeedsReconnect means the conversation's account must be
	// reconnected before inbok can reach its provider.
	ReasonNeedsReconnect Reason = "needs-reconnect"
	// ReasonFailed means the provider failed, refused, or took too long;
	// trying again may work.
	ReasonFailed Reason = "failed"
)

// Refusal is a conversation an action left as it was, and why.
type Refusal struct {
	ID     uuid.UUID
	Reason Reason
}

// Applied is what an action or a change did.
type Applied struct {
	// Conversations are the ones it changed, as they stand after, in the
	// order asked; one that no longer exists is left out.
	Conversations []Conversation
	// Refused are the ones it left as they were, in the order asked.
	Refused []Refusal
	// Undo are the changes that put each changed conversation back as it
	// was, conversations needing the same change together, in the order
	// asked (undoOf); one it left as it was is in none.
	Undo []Change
}

// ========================
// What an action needs
// ========================

// Thread is one of a member's conversations as an action reaches its
// provider.
type Thread struct {
	ID               uuid.UUID
	AccountID        int64
	ProviderThreadID string
	// ProviderNewestMessageID is the provider's ID of the conversation's
	// [Summary.NewestMessageID], which an action that is [Action.OnNewest]
	// changes.
	ProviderNewestMessageID string
}

// ActionStorage reads the conversations an action changes and writes what
// the provider answers.
type ActionStorage interface {
	// ListThreads returns userID's conversations of ids with their
	// threads and newest messages, leaving out any that is not theirs.
	ListThreads(ctx context.Context, userID int64, ids []uuid.UUID) ([]Thread, error)

	// UpdateMessages writes updates to accountID's messages and their
	// conversations, as [mailsync.Storage.UpdateSyncedMail] writes
	// [mailsync.Change.Updated], keeping the sync state. A mailbox the
	// account does not have yet is left out; the next sync adds it. It
	// returns [mailsync.ErrNotFound] when the account has no sync state.
	UpdateMessages(ctx context.Context, accountID int64, updates []mailsync.MessageUpdate) error

	// ReadConversations returns userID's conversations of ids, in the
	// order of ids, leaving out any that is not theirs.
	ReadConversations(ctx context.Context, userID int64, ids []uuid.UUID) ([]Conversation, error)

	// ListPlaceMailboxes returns the places of userID's accounts a
	// [Change] can add a conversation to or take it out of, each with its
	// mailbox at the provider: each account's inbox, Spam, and Trash, and
	// the member's own labels.
	ListPlaceMailboxes(ctx context.Context, userID int64) ([]PlaceMailbox, error)
}

// accountAccess hands out access tokens, e.g. [connectedaccount.Access].
type accountAccess interface {
	Token(ctx context.Context, accountID int64) (connectedaccount.AccessToken, error)
}

// ThreadChanger applies actions at a provider.
type ThreadChanger interface {
	// Version returns the account's current change number at the
	// provider, as [mailsync.Message.Version] counts: every change made
	// after it carries a greater one. [ActionService.Apply] reads it before
	// its first [ThreadChanger.ChangeThread] of the account.
	Version(ctx context.Context, token connectedaccount.AccessToken) (uint64, error)

	// ChangeThread applies action to thread at the provider: to its newest
	// message, [Thread.ProviderNewestMessageID], when action is
	// [Action.OnNewest], else to every message of the thread.
	// It returns where each changed message stands after, as the provider
	// answers, with [mailsync.MessageUpdate.Version] zero when the provider
	// answers without one, and an error wrapping [ErrNotFound] when the
	// provider no longer has the thread or that message.
	ChangeThread(ctx context.Context, token connectedaccount.AccessToken, thread Thread, action Action) ([]mailsync.MessageUpdate, error)

	// ModifyThread applies modification to thread at the provider as it
	// is, by none of the provider's own rules: its mailboxes to every
	// message, and a [MarkSet] mark to the newest message,
	// [Thread.ProviderNewestMessageID], as [Action.OnNewest] does, a
	// [MarkCleared] one to every message. It returns as
	// [ThreadChanger.ChangeThread] does, except that when it applied part
	// of modification before failing, it returns the updates of that part
	// with the error, as the provider kept them.
	ModifyThread(ctx context.Context, token connectedaccount.AccessToken, thread Thread, modification Modification) ([]mailsync.MessageUpdate, error)
}

// threadCall applies one action or change to thread at its provider with
// token, calling it with ctx, as [ThreadChanger.ChangeThread] returns.
type threadCall func(ctx context.Context, token connectedaccount.AccessToken, thread Thread) ([]mailsync.MessageUpdate, error)

// ========================
// Applying an action
// ========================

// actionBudget is how long an action waits on its provider at most,
// inbok's choice: a thread not changed by then is refused as failed.
var actionBudget = 7 * time.Second

// actionReserve is how much of the caller's deadline an action keeps for
// its writes and answer after the provider, inbok's choice: with a
// request's 10 seconds, [actionBudget] fits whole when the request started
// on time.
var actionReserve = 3 * time.Second

// actionConcurrency bounds one account's provider calls in flight, inbok's
// choice, as package google bounds its fetches: providers limit a user's
// concurrent requests, Gmail at a number it does not publish.
const actionConcurrency = 4

// ActionService applies a member's actions at the provider, then writes
// what it answers.
type ActionService struct {
	storage  ActionStorage
	access   accountAccess
	provider ThreadChanger
	logger   *slog.Logger
}

// NewActionService returns an [ActionService] that reads and writes with
// storage, takes tokens from access, calls provider, and logs to logger.
func NewActionService(storage ActionStorage, access accountAccess, provider ThreadChanger, logger *slog.Logger) *ActionService {
	return &ActionService{storage: storage, access: access, provider: provider, logger: logger}
}

// Apply applies action to userID's conversations of ids at their
// providers, and writes each thread's messages as the provider answers,
// one write per account, the accounts at once. It returns
// [ErrInvalidAction] and [ErrInvalidIDs] for bad input, and any other error
// as a failure of the whole action; a conversation's own failure is a
// [Refusal].
//
// DESIGN: inbok writes only what the provider answered, so it never shows
// a change the provider does not have, and copies none of the provider's
// rules, e.g. what Trash does to a thread's other labels.
func (a *ActionService) Apply(ctx context.Context, userID int64, action Action, ids []uuid.UUID) (Applied, error) {
	// 1. Refuse bad input before any read or provider call.
	if !slices.Contains(Actions, action) {
		return Applied{}, ErrInvalidAction
	}
	ids = uniqueIDs(ids)
	if len(ids) == 0 || len(ids) > MaxActionIDs {
		return Applied{}, ErrInvalidIDs
	}

	// 2. Find the member's threads; any other ID is not found.
	threads, err := a.storage.ListThreads(ctx, userID, ids)
	if err != nil {
		return Applied{}, err
	}
	reach := func(uuid.UUID) Reach { return action.Reach() }
	return a.run(ctx, userID, ids, threads, reach, func(ctx context.Context, token connectedaccount.AccessToken, thread Thread) ([]mailsync.MessageUpdate, error) {
		return a.provider.ChangeThread(ctx, token, thread, action)
	})
}

// run applies call to threads, userID's conversations of ids, at their
// providers, and writes each thread's messages as the provider answers,
// one write per account, the accounts at once. It returns what changed,
// refusing every ID with no thread as not found, and the changes that
// undo it, each within what reach says call may change of that
// conversation.
//
// DESIGN: the undo is the difference between each conversation's row
// before and after, within its reach, so it puts back only what this
// changed: a label added meanwhile elsewhere stays, and a star or unread
// mark that changed during a move, e.g. a new reply's, is not taken back.
func (a *ActionService) run(ctx context.Context, userID int64, ids []uuid.UUID, threads []Thread, reach func(uuid.UUID) Reach, call threadCall) (Applied, error) {
	reasons := make(map[uuid.UUID]Reason, len(ids))
	for _, id := range ids {
		reasons[id] = ReasonNotFound
	}
	byAccount := make(map[int64][]Thread)
	found := make([]uuid.UUID, 0, len(threads))
	for _, thread := range threads {
		delete(reasons, thread.ID)
		byAccount[thread.AccountID] = append(byAccount[thread.AccountID], thread)
		found = append(found, thread.ID)
	}

	// 1. Read the rows as they stand before any change, which the undo
	// returns to.
	before, err := a.storage.ReadConversations(ctx, userID, found)
	if err != nil {
		return Applied{}, err
	}

	// 2. Act at each account's provider, all at once, within the budget.
	outcomes := make([]accountOutcome, 0, len(byAccount))
	for accountID, accountThreads := range byAccount {
		outcomes = append(outcomes, accountOutcome{accountID: accountID, threads: accountThreads})
	}
	group, groupCtx := errgroup.WithContext(ctx)
	budget, cancel := providerBudget(groupCtx)
	defer cancel()
	for i := range outcomes {
		group.Go(func() error {
			return a.applyAccount(groupCtx, budget, call, &outcomes[i])
		})
	}
	if err := group.Wait(); err != nil {
		return Applied{}, err
	}

	// 3. Read the changed rows as they stand after every account's write.
	isChanged := make(map[uuid.UUID]bool)
	for _, outcome := range outcomes {
		for _, id := range outcome.changed {
			isChanged[id] = true
		}
		for id, reason := range outcome.refused {
			reasons[id] = reason
		}
	}
	changed := slices.DeleteFunc(slices.Clone(ids), func(id uuid.UUID) bool { return !isChanged[id] })
	rows, err := a.storage.ReadConversations(ctx, userID, changed)
	if err != nil {
		return Applied{}, err
	}
	applied := Applied{Conversations: rows, Refused: []Refusal{}, Undo: undoOf(before, rows, reach)}
	for _, id := range ids {
		if reason, ok := reasons[id]; ok {
			applied.Refused = append(applied.Refused, Refusal{ID: id, Reason: reason})
		}
	}
	return applied, nil
}

// providerBudget returns ctx ending after [actionBudget], or earlier to
// keep [actionReserve] of ctx's own deadline; already ended when that has
// passed.
func providerBudget(ctx context.Context) (context.Context, context.CancelFunc) {
	end := time.Now().Add(actionBudget)
	if deadline, ok := ctx.Deadline(); ok && deadline.Add(-actionReserve).Before(end) {
		end = deadline.Add(-actionReserve)
	}
	return context.WithDeadline(ctx, end)
}

// accountOutcome is one account's part of an action: its threads, and
// which conversations changed and which were refused.
type accountOutcome struct {
	accountID int64
	threads   []Thread
	changed   []uuid.UUID
	refused   map[uuid.UUID]Reason
}

// applyAccount applies call to outcome's threads at their provider,
// calling it within budget, writes what it answered with ctx, and records
// each thread's result in outcome. It returns an error only for a failure
// of the whole action, e.g. storage's or ctx ending.
//
// DESIGN: an update the provider answers without a version, as Gmail's
// label changes and Trash do, is written at the account's version read
// before the calls. Every version inbok held then was read earlier, so the
// write passes as not late, unless a poll stores a newer one meanwhile and
// wins; a change made after that version carries a greater one, so the
// poll that brings it still writes over this one, and the action's own
// record comes again with that poll.
func (a *ActionService) applyAccount(ctx, budget context.Context, call threadCall, outcome *accountOutcome) error {
	outcome.refused = make(map[uuid.UUID]Reason)
	refuseAll := func(reason Reason) {
		for _, thread := range outcome.threads {
			outcome.refused[thread.ID] = reason
		}
	}

	// 1. Take a token; an account its provider cannot be reached for
	// refuses every thread.
	token, err := a.access.Token(budget, outcome.accountID)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if reason, ok := tokenReason(err); ok {
		a.logger.WarnContext(ctx, "conversation action stopped", "account_id", outcome.accountID, "err", err)
		refuseAll(reason)
		return nil
	}
	if err != nil {
		return err
	}

	// 2. Read the account's version before any change, which the changes'
	// unversioned updates are written at.
	version, err := a.provider.Version(budget, token)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		a.logger.WarnContext(ctx, "conversation action failed at provider", "account_id", outcome.accountID, "err", err)
		refuseAll(ReasonFailed)
		return nil
	}

	// 3. Change each thread at the provider.
	updates := make([][]mailsync.MessageUpdate, len(outcome.threads))
	reasons := make([]Reason, len(outcome.threads))
	group := errgroup.Group{}
	group.SetLimit(actionConcurrency)
	for i, thread := range outcome.threads {
		group.Go(func() error {
			changed, err := call(budget, token, thread)
			switch {
			case ctx.Err() != nil:
				return ctx.Err()
			case err != nil && len(changed) > 0:
				// The provider kept part of the change: written, the row
				// shows where the thread stands, and the undo puts back
				// only that part.
				a.logger.WarnContext(ctx, "conversation change partly applied at provider",
					"account_id", outcome.accountID, "conversation_id", thread.ID, "err", err)
				updates[i] = changed
			case errors.Is(err, ErrNotFound):
				reasons[i] = ReasonNotFound
			case err != nil:
				a.logger.WarnContext(ctx, "conversation action failed at provider",
					"account_id", outcome.accountID, "conversation_id", thread.ID, "err", err)
				reasons[i] = ReasonFailed
			default:
				updates[i] = changed
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}

	// 4. Write every changed thread's messages at once, with ctx: the
	// provider has them now, whether or not the budget has run out.
	var written []mailsync.MessageUpdate
	for i, thread := range outcome.threads {
		if reasons[i] != "" {
			outcome.refused[thread.ID] = reasons[i]
			continue
		}
		outcome.changed = append(outcome.changed, thread.ID)
		for _, update := range updates[i] {
			if update.Version == 0 {
				update.Version = version
			}
			written = append(written, update)
		}
	}
	if len(outcome.changed) == 0 {
		return nil
	}
	err = a.storage.UpdateMessages(ctx, outcome.accountID, written)
	if errors.Is(err, mailsync.ErrNotFound) {
		// The account was removed meanwhile, with its conversations.
		for _, id := range outcome.changed {
			outcome.refused[id] = ReasonNotFound
		}
		outcome.changed = nil
		return nil
	}
	return err
}

// ========================
// Helpers
// ========================

// tokenReason returns why an account's threads are refused when taking its
// token failed with err, and false for an error that fails the action.
func tokenReason(err error) (Reason, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, connectedaccount.ErrNotFound), errors.Is(err, connectedaccount.ErrDeactivated):
		return ReasonNotFound, true
	case errors.Is(err, connectedaccount.ErrNeedsReconnect), errors.Is(err, connectedaccount.ErrTokenUnreadable):
		return ReasonNeedsReconnect, true
	case errors.Is(err, connectedaccount.ErrRefreshFailed), errors.Is(err, connectedaccount.ErrCredentialChanged),
		errors.Is(err, context.DeadlineExceeded):
		return ReasonFailed, true
	default:
		return "", false
	}
}

// uniqueIDs returns ids without repeats, in their first order.
func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	unique := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	return unique
}
