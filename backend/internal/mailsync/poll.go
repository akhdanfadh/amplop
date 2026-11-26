package mailsync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"amplop/internal/connectedaccount"
)

// Kind is which sync a run for an account does.
type Kind string

const (
	// KindPoll applies what changed at the provider since the stored
	// position.
	KindPoll Kind = "poll"
	// KindFull copies the window again, as [FullSync] does.
	KindFull Kind = "full"
)

// PollScope is which account state a poll applies changes in.
type PollScope int

const (
	// PollComplete is a poll of an account whose full sync is complete.
	PollComplete PollScope = iota + 1
	// PollDuringCopy is a poll inside a running full sync's run, so new mail
	// arrives while a long copy goes on.
	PollDuringCopy
)

// PollFrom is where a poll applies changes from, and the account state it
// expects throughout.
type PollFrom struct {
	Scope PollScope
	// Position is the stored [SyncState.Position] the poll reads changes
	// since.
	Position string
	// Generation is the running full sync's [SyncState.Generation] for
	// [PollDuringCopy]; unused for [PollComplete].
	Generation int
}

// holds reports whether state is still the one f expects: at f's position,
// and complete, or running at f's generation, as f's scope says.
func (f PollFrom) holds(state SyncState) bool {
	if state.Position != f.Position {
		return false
	}
	if f.Scope == PollDuringCopy {
		return state.Status == StatusRunning && state.Generation == f.Generation
	}
	return state.Status == StatusComplete
}

// Plan is what one sync run for an account does.
type Plan struct {
	Kind Kind
	// Position is where a poll asks for changes since: the account's
	// [SyncState.Position]. Empty for a full sync.
	Position string
	// RequestedAt is the account's [Report.RequestedAt] as the plan read
	// it, before a poll's first read: a request that changes it is
	// followed by another poll (see [Poll.Recheck]).
	RequestedAt *time.Time
}

// Recheck is whether a poll that finished polls once more.
type Recheck struct {
	// Again is whether a member asked to sync since the poll's first read
	// may have started.
	Again bool
	// RequestedAt is the account's [Report.RequestedAt] as read now,
	// before the next poll's first read.
	RequestedAt *time.Time
}

const (
	// applyFetches is the most messages one [Poll.ApplyChanges] part
	// fetches, as one full sync page does, so a step stays short however
	// many messages one history page names.
	applyFetches = 100
	// applyWrites is the most messages one part deletes or updates, as one
	// [FullSync.DeleteEarlier] deletes.
	applyWrites = deleteBatch
)

// Poll applies what changed in an account's mail at its provider since the
// stored position, in steps a workflow runs in order: [Poll.Plan], then,
// for [KindPoll], for each page of changes [Poll.ReadChanges] once and
// [Poll.ApplyChanges] for each of its parts, then [Poll.Recheck], which
// may start the same steps again from the new position. A running full sync runs the
// same steps with [PollDuringCopy] between its pages. A step that fails ends the
// poll as [EndingOf] says: [EndingResync] with a full sync in its place,
// [EndingFail] with [Poll.Fail], and any other with nothing recorded, since
// a poll leaves the account complete. Every step is safe to repeat.
//
// DESIGN: The workflow records each page of changes, IDs only, so a page
// naming thousands of messages is applied in short steps of at most
// applyFetches fetches, and a worker stopped midway resumes at its part.
// The stored position moves only with the last part of the last page, so
// a poll stopped for good starts over from it, and every part writes only
// while the account is still complete at that position: a full sync
// started since, or a poll that already finished, makes the next part stop.
type Poll struct {
	storage  Storage
	provider Provider
	access   accountAccess
	now      func() time.Time
}

// NewPoll returns a [Poll] given storage, the provider it reads, access to
// its accounts' tokens, and a clock.
func NewPoll(storage Storage, provider Provider, access accountAccess, now func() time.Time) *Poll {
	return &Poll{storage: storage, provider: provider, access: access, now: now}
}

// Plan returns what account accountID's sync run does: a poll from its
// position once its full sync is complete, and otherwise a full sync, with
// the request time it saw, which [Poll.Recheck] compares. The
// first step of either stops for a stopped account. It returns
// [ErrNotFound] when the account has no sync state.
//
// DESIGN: One sync runs for an account at a time, so the status read here
// holds until this run changes it.
func (p *Poll) Plan(ctx context.Context, accountID int64) (Plan, error) {
	requestedAt, err := p.requestedAt(ctx, accountID)
	if err != nil {
		return Plan{}, err
	}
	state, err := p.storage.SyncState(ctx, accountID)
	if err != nil {
		return Plan{}, err
	}
	if state.Status != StatusComplete {
		return Plan{Kind: KindFull, RequestedAt: requestedAt}, nil
	}
	return Plan{Kind: KindPoll, Position: state.Position, RequestedAt: requestedAt}, nil
}

// Recheck reports whether account accountID's poll, which finished, polls
// once more: its member asked to sync since the poll read seen, its
// [Report.RequestedAt] before its first read, so the poll may have missed
// mail sent before the request. It returns [ErrNotFound] when the account has
// no sync state.
//
// DESIGN: The stored value is compared, not the clocks: the request's time
// comes from the server, and the worker's clock may differ. Every request
// writes a new time, and the request limit keeps two apart, so a changed
// value means a newer request.
func (p *Poll) Recheck(ctx context.Context, accountID int64, seen *time.Time) (Recheck, error) {
	requestedAt, err := p.requestedAt(ctx, accountID)
	if err != nil {
		return Recheck{}, err
	}
	changed := requestedAt != nil && (seen == nil || !requestedAt.Equal(*seen))
	return Recheck{Again: changed, RequestedAt: requestedAt}, nil
}

// requestedAt returns account accountID's [Report.RequestedAt], or
// [ErrNotFound] when it has no sync state.
func (p *Poll) requestedAt(ctx context.Context, accountID int64) (*time.Time, error) {
	reports, err := p.storage.Reports(ctx, []int64{accountID})
	if err != nil {
		return nil, err
	}
	if len(reports) == 0 {
		return nil, ErrNotFound
	}
	return reports[0].RequestedAt, nil
}

// ReadChanges returns one page of account accountID's changes since
// position, asking with pageToken, empty for the first. It returns an error
// wrapping [ErrPositionExpired] when the provider no longer keeps changes
// since position.
func (p *Poll) ReadChanges(ctx context.Context, accountID int64, position, pageToken string) (ChangePage, error) {
	// 1. Take an access token, which stops a stopped account before any
	// provider call.
	token, err := p.access.Token(ctx, accountID)
	if err != nil {
		return ChangePage{}, err
	}

	// 2. Read the page.
	page, err := p.provider.Changes(ctx, token, position, pageToken)
	if err != nil {
		return ChangePage{}, providerFailed("read changes", err)
	}
	return page, nil
}

// ApplyChanges applies part part, counted from 0, of page, the page of
// account accountID's changes since from's position that [Poll.ReadChanges]
// read with pageToken. A part deletes up to applyWrites messages, writes
// where up to applyWrites updated messages stand, or fetches up to
// applyFetches messages: those added are saved, and those reread are applied
// as updates. An update to a message the copy does not hold is skipped: only
// mail older than the account's first window is not held. The first part of
// the first page also lists the mailboxes, whose creation, renaming, and
// deletion the changes do not name. The last part of the last page records
// the position after it; for [PollComplete], also the sync as finished now
// without a failure, which a full sync's [FullSync.Finish] records for
// [PollDuringCopy]. A message saved takes the account's generation, so the
// sweep of a running full sync keeps it. It reports whether more parts
// follow, and returns [ErrSuperseded] when the account is no longer as from
// expects.
func (p *Poll) ApplyChanges(ctx context.Context, accountID int64, from PollFrom, pageToken string, page ChangePage, part int) (bool, error) {
	parts := pageParts(page)
	if part < 0 || part >= len(parts) {
		return false, fmt.Errorf("part %d of a page of %d", part, len(parts))
	}
	work := parts[part]
	listMailboxes := pageToken == "" && part == 0

	// 1. Stop for a stopped account before any provider call or write:
	// with a token when the part calls the provider.
	var token connectedaccount.AccessToken
	var err error
	if listMailboxes || len(work.fetch) > 0 {
		token, err = p.access.Token(ctx, accountID)
	} else {
		err = p.access.Check(ctx, accountID)
	}
	if err != nil {
		return false, err
	}

	// 2. List the mailboxes on the first part of the first page.
	var mailboxes []Mailbox
	if listMailboxes {
		mailboxes, err = p.mailboxes(ctx, token, accountID)
		if err != nil {
			return false, err
		}
	}

	// 3. Fetch the part's messages. One added is saved whatever its received
	// time: mail imported into the account is new to the member. One reread
	// is applied as an update, so one the copy does not hold stays out.
	change := Change{Deleted: work.deleted, Updated: work.updated}
	if len(work.fetch) > 0 {
		fetched, err := p.provider.Messages(ctx, token, accountID, work.fetch)
		if err != nil {
			return false, providerFailed("fetch messages", err)
		}
		for _, message := range fetched.Messages {
			if !work.reread[message.ProviderID] {
				change.Saved = append(change.Saved, message)
				continue
			}
			change.Updated = append(change.Updated, MessageUpdate{
				ProviderID:         message.ProviderID,
				MailboxProviderIDs: message.MailboxProviderIDs,
				Seen:               message.Seen,
				Flagged:            message.Flagged,
				Version:            message.Version,
			})
		}
		change.Deleted = append(change.Deleted, fetched.Gone...)
	}

	// 4. Write the part, while the account is still as from expects. The
	// last part may find its own position already written: it committed
	// before its step was recorded, and runs again on a resumed run, which
	// would otherwise leave a running full sync stranded.
	last := page.NextPageToken == "" && part == len(parts)-1
	done := from
	done.Position = page.Position
	write := func(change Change) error {
		_, err := p.storage.UpdateSyncedMail(ctx, accountID, func(current SyncState) (Change, error) {
			if !from.holds(current) && !(last && done.holds(current)) {
				return Change{}, ErrSuperseded
			}
			change.State = current
			if last {
				change.State.Position = page.Position
			}
			if last && from.Scope == PollComplete {
				now := p.now().UTC()
				change.State.LastSyncedAt = &now
				change.State.Failure = ""
				change.State.FailedAt = nil
			}
			return change, nil
		})
		return err
	}
	change.Mailboxes = mailboxes
	err = write(change)
	if errors.Is(err, ErrUnknownMailbox) {
		// A message names a mailbox made since they were listed, or one
		// deleted since the change was recorded: listing them again mends
		// the first, and leaving out what is still unknown the second.
		if token.Value() == "" {
			token, err = p.access.Token(ctx, accountID)
			if err != nil {
				return false, err
			}
		}
		mailboxes, err = p.mailboxes(ctx, token, accountID)
		if err != nil {
			return false, err
		}
		change.Mailboxes = mailboxes
		change.Saved, change.Updated = withKnownMailboxes(mailboxes, change.Saved, change.Updated)
		err = write(change)
	}
	if err != nil {
		return false, err
	}
	return part+1 < len(parts), nil
}

// Fail records that account accountID's poll from position failed with
// failure, now, keeping the account complete. A poll superseded, or an
// account gone, records nothing.
func (p *Poll) Fail(ctx context.Context, accountID int64, position string, failure Failure) error {
	_, err := p.storage.UpdateSyncedMail(ctx, accountID, func(current SyncState) (Change, error) {
		if current.Status != StatusComplete || current.Position != position {
			return Change{}, ErrSuperseded
		}
		now := p.now().UTC()
		state := current
		state.Failure = failure
		state.FailedAt = &now
		return Change{State: state}, nil
	})
	if errors.Is(err, ErrSuperseded) || errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// mailboxes returns account accountID's mailboxes, listed with token; an
// empty list for none, which deletes the saved ones.
func (p *Poll) mailboxes(ctx context.Context, token connectedaccount.AccessToken, accountID int64) ([]Mailbox, error) {
	mailboxes, err := p.provider.Mailboxes(ctx, token)
	if err != nil {
		return nil, providerFailed("list mailboxes", err)
	}
	if mailboxes == nil {
		mailboxes = []Mailbox{}
	}
	return mailboxes, nil
}

// pagePart is one part of a page of changes, as [Poll.ApplyChanges]
// applies it: messages to delete, updates to write, or messages to fetch,
// those in reread to apply as updates.
type pagePart struct {
	deleted []string
	updated []MessageUpdate
	fetch   []string
	reread  map[string]bool
}

// pageParts returns page's parts, in order: its deletes and its updates,
// applyWrites at a time, then its messages added and reread, applyFetches
// at a time. A page that changed nothing has one empty part, which still
// moves the position.
func pageParts(page ChangePage) []pagePart {
	var parts []pagePart
	for chunk := range slices.Chunk(page.Deleted, applyWrites) {
		parts = append(parts, pagePart{deleted: chunk})
	}
	for chunk := range slices.Chunk(page.Update, applyWrites) {
		parts = append(parts, pagePart{updated: chunk})
	}
	for chunk := range slices.Chunk(page.Fetch, applyFetches) {
		parts = append(parts, pagePart{fetch: chunk})
	}
	for chunk := range slices.Chunk(page.Reread, applyFetches) {
		reread := make(map[string]bool, len(chunk))
		for _, id := range chunk {
			reread[id] = true
		}
		parts = append(parts, pagePart{fetch: chunk, reread: reread})
	}
	if len(parts) == 0 {
		parts = append(parts, pagePart{})
	}
	return parts
}

// withKnownMailboxes returns saved and updated naming only the mailboxes
// listed in mailboxes: a mailbox a change names that the provider no longer
// lists was deleted since, and so left every message.
func withKnownMailboxes(mailboxes []Mailbox, saved []Message, updated []MessageUpdate) ([]Message, []MessageUpdate) {
	known := make(map[string]bool, len(mailboxes))
	for _, mailbox := range mailboxes {
		known[mailbox.ProviderID] = true
	}
	keep := func(ids []string) []string {
		return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return !known[id] })
	}
	saved = slices.Clone(saved)
	for i := range saved {
		saved[i].MailboxProviderIDs = keep(saved[i].MailboxProviderIDs)
	}
	updated = slices.Clone(updated)
	for i := range updated {
		updated[i].MailboxProviderIDs = keep(updated[i].MailboxProviderIDs)
	}
	return saved, updated
}
