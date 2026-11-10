package mailsync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"inbok/internal/connectedaccount"
)

const (
	// window is how far back a full sync copies, by the provider's own
	// timestamp (US026; ADR001).
	window = 90 * 24 * time.Hour
	// deleteBatch is the most earlier messages one [FullSync.DeleteEarlier]
	// deletes, inbok's choice, so one transaction stays short however much
	// the provider deleted, and the content and search work later branches
	// add to it stays bounded.
	deleteBatch = 1000
)

// ErrSuperseded means the account's sync state no longer belongs to the full
// sync that asked: a newer one started, or this one already ended.
var ErrSuperseded = errors.New("the account's full sync is no longer this one")

// errProvider marks an error from a [Provider] call.
var errProvider = errors.New("provider failed")

// accountAccess hands out access tokens for connected accounts, forgets
// one the provider refused, and says whether their provider work may still
// run, e.g. [connectedaccount.Access].
type accountAccess interface {
	Token(ctx context.Context, accountID int64) (connectedaccount.AccessToken, error)
	Forget(accountID int64)
	Check(ctx context.Context, accountID int64) error
}

// providerFailed returns err, from the provider call what for account
// accountID, marked as the provider's. When the provider refused the access
// token, it first makes access forget it, so the next try takes a new one.
func providerFailed(access accountAccess, accountID int64, what string, err error) error {
	if errors.Is(err, ErrUnauthorized) {
		access.Forget(accountID)
	}
	return fmt.Errorf("%w: %s: %w", errProvider, what, err)
}

// Run is one full sync of an account, as each of its steps shares it.
type Run struct {
	// Generation is the full sync's [SyncState.Generation]; 0 before
	// [FullSync.Start] gave it one.
	Generation int
	// Since is where the window starts: the full sync copies the messages
	// the provider received after it.
	Since time.Time
}

// Ending is how an error from a [FullSync] step ends the full sync.
type Ending int

const (
	// EndingRetry means trying the step again may succeed: the provider or
	// inbok failed. Past its retries, the full sync fails with [FailureOf].
	EndingRetry Ending = iota + 1
	// EndingWait means the full sync waits for the member: the account needs
	// reconnecting, or its member is deactivated. [FullSync.Wait] records it.
	EndingWait
	// EndingGone means nothing is left to record: the account was
	// disconnected, or the full sync was superseded.
	EndingGone
	// EndingFail means trying again cannot help, so the full sync fails now
	// with [FailureOf], e.g. on a refresh token no key opens.
	EndingFail
)

// EndingOf returns how err, from a [FullSync] step, ends the full sync.
func EndingOf(err error) Ending {
	switch {
	case errors.Is(err, connectedaccount.ErrNeedsReconnect), errors.Is(err, connectedaccount.ErrDeactivated):
		return EndingWait
	case errors.Is(err, connectedaccount.ErrNotFound), errors.Is(err, ErrNotFound), errors.Is(err, ErrSuperseded):
		return EndingGone
	case errors.Is(err, connectedaccount.ErrTokenUnreadable):
		return EndingFail
	default:
		return EndingRetry
	}
}

// FailureOf returns the [Failure] err, from a [FullSync] step, records.
func FailureOf(err error) Failure {
	switch {
	case errors.Is(err, connectedaccount.ErrTokenUnreadable):
		return FailureTokenUnreadable
	case errors.Is(err, errProvider), errors.Is(err, ErrProviderBusy), errors.Is(err, connectedaccount.ErrRefreshFailed):
		return FailureProvider
	default:
		return FailureInternal
	}
}

// FullSync copies the last 90 days of an account's mail from its provider,
// newest first, in steps a workflow runs in order: [FullSync.Start], then
// [FullSync.CopyPage] until the listing ends, then [FullSync.DeleteEarlier]
// until none are left, then [FullSync.Finish]. The window reaches back to
// the oldest message the copy holds, if that is older, so a full sync run
// again after the provider lost the changes brings every stored message
// current (ADR001, "Full sync"). A step that fails ends the
// full sync with [FullSync.Wait] or [FullSync.Fail], as [EndingOf] says.
// Every step is safe to repeat, and each first checks that the account's
// provider work may still run: a step that calls the provider takes its own
// access token, so no step's output holds one.
//
// DESIGN: A step writes only while the account's sync state still has the
// step's generation, so a full sync that a newer one superseded stops at its
// next step. The workflow keeps one full sync per account at a time.
type FullSync struct {
	storage  Storage
	provider Provider
	access   accountAccess
	now      func() time.Time
}

// NewFullSync returns a [FullSync] given storage, the provider it reads,
// access to its accounts' tokens, and a clock.
func NewFullSync(storage Storage, provider Provider, access accountAccess, now func() time.Time) *FullSync {
	return &FullSync{storage: storage, provider: provider, access: access, now: now}
}

// Start begins account accountID's full sync: it saves the account's
// mailboxes and current position, and a new generation, running, with no
// progress. It returns the run its other steps share, whose window starts
// window before now, or just before the oldest message the copy holds if
// that is earlier.
func (s *FullSync) Start(ctx context.Context, accountID int64) (Run, error) {
	// 1. Take an access token, which stops a stopped account before any
	// provider call.
	token, err := s.access.Token(ctx, accountID)
	if err != nil {
		return Run{}, err
	}

	// 2. List the mailboxes, which the pages' messages name.
	mailboxes, err := s.provider.Mailboxes(ctx, token)
	if err != nil {
		return Run{}, providerFailed(s.access, accountID, "list mailboxes", err)
	}
	// nil would leave the saved mailboxes; none listed means none left.
	if mailboxes == nil {
		mailboxes = []Mailbox{}
	}

	// 3. Read the position before any page is listed, so the first poll
	// replays every change made while the full sync ran (ADR001).
	position, err := s.provider.Position(ctx, token)
	if err != nil {
		return Run{}, providerFailed(s.access, accountID, "read position", err)
	}

	// 4. Reach back to the oldest stored message, so a full sync run again
	// lists every message the copy holds and brings each current: the
	// provider's changes since the last poll may be lost (ADR001).
	since := s.now().UTC().Add(-window)
	oldest, ok, err := s.storage.OldestMessage(ctx, accountID)
	if err != nil {
		return Run{}, err
	}
	// The pages keep messages received after since, so it sits a second
	// before the oldest one.
	if ok && !oldest.After(since) {
		since = oldest.Add(-time.Second)
	}

	// 5. Save them with a new generation, running, with no progress.
	written, err := s.storage.UpdateSyncedMail(ctx, accountID, func(current SyncState) (Change, error) {
		state := current
		state.Status = StatusRunning
		state.Position = position
		state.Generation = current.Generation + 1
		state.PagesSynced = 0
		state.MessagesSynced = 0
		return Change{State: state, Mailboxes: mailboxes}, nil
	})
	if err != nil {
		return Run{}, err
	}
	return Run{Generation: written.State.Generation, Since: since}, nil
}

// CopyPage copies listing page page, counted from 0, of run's window for
// account accountID, asking with pageToken, empty for the first. It saves
// the messages listed and deletes the ones gone before their fetch, and it
// counts the page, once however often it repeats, in the progress. It
// returns the next page's token, empty after the last, and [ErrSuperseded]
// when run is no longer the account's full sync.
func (s *FullSync) CopyPage(ctx context.Context, accountID int64, run Run, page int, pageToken string) (string, error) {
	// 1. Take an access token, which stops a stopped account before any
	// provider call.
	token, err := s.access.Token(ctx, accountID)
	if err != nil {
		return "", err
	}

	// 2. List the page and fetch its messages' metadata. Spam and Trash are
	// copied too, so moving a message in or out of them is a mailbox change
	// (ADR001).
	query := ListQuery{Since: run.Since, IncludeJunkAndTrash: true, PageToken: pageToken}
	listed, err := s.provider.ListMessages(ctx, token, query)
	if err != nil {
		return "", providerFailed(s.access, accountID, "list messages", err)
	}
	fetched, err := s.provider.Messages(ctx, token, accountID, listed.ProviderIDs)
	if err != nil {
		return "", providerFailed(s.access, accountID, "fetch messages", err)
	}

	// 3. Keep the messages inside the window.
	saved := make([]Message, 0, len(fetched.Messages))
	for _, message := range fetched.Messages {
		// A provider may list by a coarser time than it stamps, e.g. Gmail's
		// after: in whole seconds, so the window's edge is checked here.
		if message.InternalDate.After(run.Since) {
			saved = append(saved, message)
		}
	}

	// 4. Save them, while the run is still the account's, counting the page
	// once however often this step runs.
	write := func(mailboxes []Mailbox) error {
		_, err := s.storage.UpdateSyncedMail(ctx, accountID, func(current SyncState) (Change, error) {
			if !isRunning(current, run) {
				return Change{}, ErrSuperseded
			}
			state := current
			if page >= current.PagesSynced {
				state.PagesSynced = page + 1
				state.MessagesSynced = current.MessagesSynced + len(saved)
			}
			return Change{State: state, Mailboxes: mailboxes, Saved: saved, Deleted: fetched.Gone}, nil
		})
		return err
	}
	err = write(nil)
	if errors.Is(err, ErrUnknownMailbox) {
		// A message names a mailbox made since Start listed them; listing
		// them again mends it.
		mailboxes, listErr := s.provider.Mailboxes(ctx, token)
		if listErr != nil {
			return "", providerFailed(s.access, accountID, "list mailboxes", listErr)
		}
		if mailboxes == nil {
			mailboxes = []Mailbox{}
		}
		err = write(mailboxes)
	}
	if err != nil {
		return "", err
	}
	return listed.NextPageToken, nil
}

// DeleteEarlier deletes up to deleteBatch of account accountID's messages
// that no step of run saved: the provider no longer lists them in the
// window. It reports whether more may be left, and returns [ErrSuperseded]
// when run is no longer the account's full sync, or a stop error of
// [connectedaccount.Access.Check] when its provider work stopped.
func (s *FullSync) DeleteEarlier(ctx context.Context, accountID int64, run Run) (bool, error) {
	// 1. Stop for a stopped account before deleting anything, so a member
	// deactivated after the last page keeps their copy as it was.
	if err := s.access.Check(ctx, accountID); err != nil {
		return false, err
	}

	// 2. Delete a batch, while the run is still the account's.
	written, err := s.storage.UpdateSyncedMail(ctx, accountID, func(current SyncState) (Change, error) {
		if !isRunning(current, run) {
			return Change{}, ErrSuperseded
		}
		return Change{State: current, DeleteEarlier: deleteBatch}, nil
	})
	if err != nil {
		return false, err
	}
	// A short batch means none were left.
	return written.EarlierDeleted == deleteBatch, nil
}

// Finish records account accountID's full sync run as complete, and the
// last sync as finished now, without a failure. It returns [ErrSuperseded]
// when run is no longer the account's full sync, or a stop error of
// [connectedaccount.Access.Check] when its provider work stopped.
func (s *FullSync) Finish(ctx context.Context, accountID int64, run Run) error {
	// 1. Stop for a stopped account before recording the sync complete, so
	// it ends waiting whichever step it reached.
	if err := s.access.Check(ctx, accountID); err != nil {
		return err
	}

	// 2. Record it complete, while the run is still the account's.
	_, err := s.storage.UpdateSyncedMail(ctx, accountID, func(current SyncState) (Change, error) {
		if !isRunning(current, run) {
			return Change{}, ErrSuperseded
		}
		now := s.now().UTC()
		state := current
		state.Status = StatusComplete
		state.Failure = ""
		state.FailedAt = nil
		state.LastSyncedAt = &now
		return Change{State: state}, nil
	})
	return err
}

// Wait records that account accountID's full sync run waits for the member,
// as [EndingWait] says: its status goes back to waiting. A run superseded,
// or an account gone, records nothing.
func (s *FullSync) Wait(ctx context.Context, accountID int64, run Run) error {
	return s.end(ctx, accountID, run, func(state SyncState) SyncState {
		state.Status = StatusWaiting
		return state
	})
}

// Fail records that account accountID's full sync run failed with failure,
// now. A run superseded, or an account gone, records nothing.
func (s *FullSync) Fail(ctx context.Context, accountID int64, run Run, failure Failure) error {
	return s.end(ctx, accountID, run, func(state SyncState) SyncState {
		now := s.now().UTC()
		state.Status = StatusFailed
		state.Failure = failure
		state.FailedAt = &now
		return state
	})
}

// end records the state ended returns for account accountID's full sync
// run, unless run was superseded or the account is gone.
func (s *FullSync) end(ctx context.Context, accountID int64, run Run, ended func(SyncState) SyncState) error {
	_, err := s.storage.UpdateSyncedMail(ctx, accountID, func(current SyncState) (Change, error) {
		// A run that ended before Start gave it a generation ends the
		// account's state as it is: one full sync runs at a time.
		if run.Generation != 0 && !isRunning(current, run) {
			return Change{}, ErrSuperseded
		}
		return Change{State: ended(current)}, nil
	})
	if errors.Is(err, ErrSuperseded) || errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// isRunning reports whether state is still run's: its generation, running.
func isRunning(state SyncState, run Run) bool {
	return state.Generation == run.Generation && state.Status == StatusRunning
}
