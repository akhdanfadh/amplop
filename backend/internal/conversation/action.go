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
// API. Each has an opposite, which undoes it.
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
)

// Actions lists every action, each beside its opposite.
var Actions = []Action{
	ActionRead, ActionUnread, ActionArchive, ActionUnarchive,
	ActionTrash, ActionUntrash, ActionStar, ActionUnstar,
}

// opposites pairs each action with the one that undoes it.
var opposites = map[Action]Action{
	ActionRead: ActionUnread, ActionUnread: ActionRead,
	ActionArchive: ActionUnarchive, ActionUnarchive: ActionArchive,
	ActionTrash: ActionUntrash, ActionUntrash: ActionTrash,
	ActionStar: ActionUnstar, ActionUnstar: ActionStar,
}

// Opposite returns the action that undoes a, or empty for no action.
func (a Action) Opposite() Action {
	return opposites[a]
}

// MaxActionIDs is how many conversations one action changes at most,
// inbok's choice: a page at [MaxLimit]. Each costs a Gmail call of 10 or 20
// of the member's 6,000 quota units a minute (Gmail's usage limits), so
// one action spends at most a third of a minute's.
const MaxActionIDs = MaxLimit

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

// Applied is what an action did.
type Applied struct {
	// Conversations are the ones it changed, as they stand after, in the
	// order asked; one that no longer exists is left out.
	Conversations []Conversation
	// Refused are the ones it left as they were, in the order asked.
	Refused []Refusal
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
}

// ActionStorage reads the conversations an action changes and writes what
// the provider answers.
type ActionStorage interface {
	// ListThreads returns userID's conversations of ids with their
	// threads, leaving out any that is not theirs.
	ListThreads(ctx context.Context, userID int64, ids []uuid.UUID) ([]Thread, error)

	// UpdateMessageLabels writes updates to accountID's messages and their
	// conversations, as [mailsync.Storage.UpdateSyncedMail] writes
	// [mailsync.Change.Updated], keeping the sync state. A mailbox the
	// account does not have yet is left out; the next sync adds it. It
	// returns [mailsync.ErrNotFound] when the account has no sync state.
	UpdateMessageLabels(ctx context.Context, accountID int64, updates []mailsync.MessageUpdate) error

	// ReadConversations returns userID's conversations of ids, in the
	// order of ids, leaving out any that is not theirs.
	ReadConversations(ctx context.Context, userID int64, ids []uuid.UUID) ([]Conversation, error)
}

// accountAccess hands out access tokens, e.g. [connectedaccount.Access].
type accountAccess interface {
	Token(ctx context.Context, accountID int64) (connectedaccount.AccessToken, error)
}

// ThreadChanger applies actions at a provider.
type ThreadChanger interface {
	// ChangeThread applies action to every message of the thread with
	// threadID at the provider, and returns where each stands after, as
	// the provider answers. It returns an error wrapping [ErrNotFound]
	// when the provider no longer has the thread.
	ChangeThread(ctx context.Context, token connectedaccount.AccessToken, threadID string, action Action) ([]mailsync.MessageUpdate, error)
}

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
// choice, as package google bounds its fetches: Gmail limits a user's
// concurrent requests at a number it does not publish.
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
// rules, e.g. what Trash does to a thread's other labels (ADR031, "Mail
// actions as built").
func (a *ActionService) Apply(ctx context.Context, userID int64, action Action, ids []uuid.UUID) (Applied, error) {
	// 1. Refuse bad input before any read or provider call.
	if action.Opposite() == "" {
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
	reasons := make(map[uuid.UUID]Reason, len(ids))
	for _, id := range ids {
		reasons[id] = ReasonNotFound
	}
	byAccount := make(map[int64][]Thread)
	for _, thread := range threads {
		delete(reasons, thread.ID)
		byAccount[thread.AccountID] = append(byAccount[thread.AccountID], thread)
	}

	// 3. Act at each account's provider, all at once, within the budget.
	outcomes := make([]accountOutcome, 0, len(byAccount))
	for accountID, accountThreads := range byAccount {
		outcomes = append(outcomes, accountOutcome{accountID: accountID, threads: accountThreads})
	}
	group, groupCtx := errgroup.WithContext(ctx)
	budget, cancel := providerBudget(groupCtx)
	defer cancel()
	for i := range outcomes {
		group.Go(func() error {
			return a.applyAccount(groupCtx, budget, action, &outcomes[i])
		})
	}
	if err := group.Wait(); err != nil {
		return Applied{}, err
	}

	// 4. Read the changed rows as they stand after every account's write.
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
	applied := Applied{Conversations: rows, Refused: []Refusal{}}
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

// applyAccount applies action to outcome's threads at their provider,
// calling it within budget, writes what it answered with ctx, and records
// each thread's result in outcome. It returns an error only for a failure
// of the whole action, e.g. storage's or ctx ending.
func (a *ActionService) applyAccount(ctx, budget context.Context, action Action, outcome *accountOutcome) error {
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

	// 2. Change each thread at the provider.
	updates := make([][]mailsync.MessageUpdate, len(outcome.threads))
	reasons := make([]Reason, len(outcome.threads))
	group := errgroup.Group{}
	group.SetLimit(actionConcurrency)
	for i, thread := range outcome.threads {
		group.Go(func() error {
			changed, err := a.provider.ChangeThread(budget, token, thread.ProviderThreadID, action)
			switch {
			case ctx.Err() != nil:
				return ctx.Err()
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

	// 3. Write every changed thread's messages at once, with ctx: the
	// provider has them now, whether or not the budget has run out.
	var written []mailsync.MessageUpdate
	for i, thread := range outcome.threads {
		if reasons[i] != "" {
			outcome.refused[thread.ID] = reasons[i]
			continue
		}
		outcome.changed = append(outcome.changed, thread.ID)
		written = append(written, updates[i]...)
	}
	if len(outcome.changed) == 0 {
		return nil
	}
	err = a.storage.UpdateMessageLabels(ctx, outcome.accountID, written)
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
