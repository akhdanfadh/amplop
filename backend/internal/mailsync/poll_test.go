package mailsync

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// complete100 is a poll of a complete account at position 100.
var complete100 = PollFrom{Scope: PollComplete, Position: "100"}

var pollMailboxes = []Mailbox{
	{ProviderID: "INBOX", Name: "INBOX", Role: RoleInbox},
	{ProviderID: "Label_work", Name: "Work"},
}

// newPollTest returns a poll of syncAccount, complete at position 100 with
// the stored messages given, against provider.
func newPollTest(provider *fakeProvider, stored ...Message) (*Poll, *fakeStorage) {
	failedAt := syncNow.Add(-time.Hour)
	storage := newFakeStorage(SyncState{Status: StatusComplete, Generation: 1, Position: "100", Failure: FailureProvider, FailedAt: &failedAt})
	storage.mailboxes = pollMailboxes
	for _, m := range stored {
		storage.messages[m.ProviderID] = storedMessage{message: m, generation: 1}
	}
	return NewPoll(storage, provider, fakeAccess{}, func() time.Time { return syncNow }), storage
}

// runPoll runs every page of a poll of syncAccount from position, each
// read once and applied part by part, as the workflow does.
func runPoll(t *testing.T, p *Poll, position string) {
	t.Helper()
	token := ""
	for {
		page, err := p.ReadChanges(t.Context(), syncAccount, position, token)
		require.NoError(t, err)
		for part, more := 0, true; more; part++ {
			more, err = p.ApplyChanges(t.Context(), syncAccount, PollFrom{Scope: PollComplete, Position: position}, token, page, part)
			require.NoError(t, err)
		}
		if page.NextPageToken == "" {
			return
		}
		token = page.NextPageToken
	}
}

// count returns how many of f's calls were call.
func (f *fakeProvider) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

func TestPollPlan(t *testing.T) {
	for name, tc := range map[string]struct {
		status Status
		plan   Plan
	}{
		"complete": {status: StatusComplete, plan: Plan{Kind: KindPoll, Position: "100"}},
		"waiting":  {status: StatusWaiting, plan: Plan{Kind: KindFull}},
		"running":  {status: StatusRunning, plan: Plan{Kind: KindFull}},
		"failed":   {status: StatusFailed, plan: Plan{Kind: KindFull}},
	} {
		t.Run(name, func(t *testing.T) {
			storage := newFakeStorage(SyncState{Status: tc.status, Position: "100"})
			p := NewPoll(storage, &fakeProvider{}, fakeAccess{}, func() time.Time { return syncNow })

			plan, err := p.Plan(t.Context(), syncAccount)

			require.NoError(t, err)
			assert.Equal(t, tc.plan, plan)
		})
	}
}

func TestPoll(t *testing.T) {
	// Given stored messages, and two pages of changes: one adds a message
	// and one gone before its fetch, moves a stored message to Work and
	// reads it, updates one the copy never held, deletes another, and
	// names two to reread, one stored and one never held; the next stars
	// the last stored one.
	reread := inboxMessage("reread", syncNow.Add(-time.Hour))
	reread.MailboxProviderIDs = []string{"Label_work"}
	provider := &fakeProvider{
		mailboxes: pollMailboxes,
		messages: map[string]Message{
			"added":        inboxMessage("added", syncNow),
			"reread":       reread,
			"never-stored": inboxMessage("never-stored", syncNow.Add(-200*24*time.Hour)),
		},
		changes: []ChangePage{
			{
				Fetch: []string{"added", "gone-before-fetch"},
				Update: []MessageUpdate{
					{ProviderID: "moved", MailboxProviderIDs: []string{"Label_work"}, Seen: true},
					{ProviderID: "never-stored", MailboxProviderIDs: []string{"INBOX"}},
				},
				Reread:   []string{"reread", "never-stored"},
				Deleted:  []string{"deleted"},
				Position: "150",
			},
			{
				Update:   []MessageUpdate{{ProviderID: "starred", MailboxProviderIDs: []string{"INBOX"}, Flagged: true}},
				Position: "200",
			},
		},
	}
	p, storage := newPollTest(provider,
		inboxMessage("moved", syncNow.Add(-time.Hour)),
		inboxMessage("deleted", syncNow.Add(-time.Hour)),
		inboxMessage("starred", syncNow.Add(-time.Hour)),
		inboxMessage("reread", syncNow.Add(-time.Hour)),
	)

	runPoll(t, p, "100")

	assert.Equal(t, []string{"added", "moved", "reread", "starred"}, storage.saved(), "an unstored message stays out, reread or updated")
	moved := storage.stored("moved").message
	assert.Equal(t, []string{"Label_work"}, moved.MailboxProviderIDs)
	assert.True(t, moved.Seen)
	assert.Equal(t, []string{"Label_work"}, storage.stored("reread").message.MailboxProviderIDs)
	assert.Equal(t, "Subject reread", storage.stored("reread").message.Subject, "a reread changes only the state")
	assert.True(t, storage.stored("starred").message.Flagged)
	state := storage.current()
	assert.Equal(t, "200", state.Position, "the position after the last page")
	assert.Equal(t, StatusComplete, state.Status)
	assert.Empty(t, state.Failure, "a poll that succeeds clears the failure")
	assert.Nil(t, state.FailedAt)
	require.NotNil(t, state.LastSyncedAt)
	assert.Equal(t, syncNow, *state.LastSyncedAt)
	assert.Equal(t, 1, provider.count("mailboxes"), "mailboxes on the first page only")
	assert.Equal(t, 2, provider.count("messages"), "one fetch for the added, one for the reread")
	assert.Equal(t, 2, provider.count("changes 100"), "each page read once")
}

// A page is cut into parts: deletes and updates applyWrites at a time,
// messages to fetch or reread applyFetches at a time, and one empty part
// for a page that changed nothing.
func TestPollParts(t *testing.T) {
	ids := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s-%d", prefix, i)
		}
		return out
	}
	updates := make([]MessageUpdate, applyWrites)
	page := ChangePage{
		Deleted: ids("d", applyWrites+1),
		Update:  updates,
		Fetch:   ids("f", 2*applyFetches+1),
		Reread:  ids("r", 1),
	}

	parts := pageParts(page)

	var sizes []int
	for _, part := range parts {
		sizes = append(sizes, len(part.deleted)+len(part.updated)+len(part.fetch))
	}
	assert.Equal(t, []int{applyWrites, 1, applyWrites, applyFetches, applyFetches, 1, 1}, sizes)
	assert.Nil(t, parts[3].reread)
	assert.Equal(t, map[string]bool{"r-0": true}, parts[6].reread)
	assert.Len(t, pageParts(ChangePage{}), 1)
}

// A part out of range writes nothing.
func TestPollPartRange(t *testing.T) {
	p, _ := newPollTest(&fakeProvider{mailboxes: pollMailboxes})

	_, err := p.ApplyChanges(t.Context(), syncAccount, complete100, "", ChangePage{Position: "200"}, 1)

	require.Error(t, err)
}

// The position moves only with the last part of the last page, so a poll
// stopped midway starts over from it.
func TestPollPositionHeld(t *testing.T) {
	provider := &fakeProvider{
		mailboxes: pollMailboxes,
		messages:  map[string]Message{"added": inboxMessage("added", syncNow)},
		changes:   []ChangePage{{Deleted: []string{"x"}, Fetch: []string{"added"}, Position: "150"}, {Position: "200"}},
	}
	p, storage := newPollTest(provider)
	page, err := p.ReadChanges(t.Context(), syncAccount, "100", "")
	require.NoError(t, err)

	more, err := p.ApplyChanges(t.Context(), syncAccount, complete100, "", page, 0)
	require.NoError(t, err)
	more2, err := p.ApplyChanges(t.Context(), syncAccount, complete100, "", page, 1)
	require.NoError(t, err)

	assert.True(t, more)
	assert.False(t, more2, "the page's last part")
	assert.Equal(t, "page-1", page.NextPageToken)
	assert.Equal(t, "100", storage.current().Position)
	assert.Equal(t, FailureProvider, storage.current().Failure, "the failure stays until the poll succeeds")
}

// On the last page too, the position moves only with its last part, so
// a poll stopped between parts fetches the rest again.
func TestPollLastPageParts(t *testing.T) {
	provider := &fakeProvider{
		mailboxes: pollMailboxes,
		messages:  map[string]Message{"added": inboxMessage("added", syncNow)},
		changes:   []ChangePage{{Deleted: []string{"x"}, Fetch: []string{"added"}, Position: "200"}},
	}
	p, storage := newPollTest(provider)
	page, err := p.ReadChanges(t.Context(), syncAccount, "100", "")
	require.NoError(t, err)

	_, err = p.ApplyChanges(t.Context(), syncAccount, complete100, "", page, 0)
	require.NoError(t, err)
	between := storage.current().Position
	_, err = p.ApplyChanges(t.Context(), syncAccount, complete100, "", page, 1)
	require.NoError(t, err)

	assert.Equal(t, "100", between)
	assert.Equal(t, "200", storage.current().Position)
	assert.Equal(t, []string{"added"}, storage.saved())
}

// A page writes only while the account is complete at the poll's
// position.
func TestPollSuperseded(t *testing.T) {
	for name, state := range map[string]SyncState{
		"full sync started": {Status: StatusRunning, Generation: 2, Position: "100"},
		"position moved":    {Status: StatusComplete, Generation: 1, Position: "200"},
	} {
		t.Run(name, func(t *testing.T) {
			provider := &fakeProvider{
				mailboxes: pollMailboxes,
				messages:  map[string]Message{"added": inboxMessage("added", syncNow)},
				changes:   []ChangePage{{Fetch: []string{"added"}, Position: "300"}},
			}
			storage := newFakeStorage(state)
			p := NewPoll(storage, provider, fakeAccess{}, func() time.Time { return syncNow })

			page, err := p.ReadChanges(t.Context(), syncAccount, "100", "")
			require.NoError(t, err)
			_, err = p.ApplyChanges(t.Context(), syncAccount, complete100, "", page, 0)

			require.ErrorIs(t, err, ErrSuperseded)
			assert.Equal(t, EndingGone, EndingOf(err))
			assert.Empty(t, storage.saved())
		})
	}
}

// A mailbox made since the first page is listed again when a later page
// names it, even in a part that calls nothing else at the provider.
func TestPollNewMailbox(t *testing.T) {
	provider := &fakeProvider{
		mailboxes: pollMailboxes,
		changes: []ChangePage{
			{Position: "150"},
			{Update: []MessageUpdate{{ProviderID: "moved", MailboxProviderIDs: []string{"Label_new"}}}, Position: "200"},
		},
	}
	p, storage := newPollTest(provider, inboxMessage("moved", syncNow.Add(-time.Hour)))
	first, err := p.ReadChanges(t.Context(), syncAccount, "100", "")
	require.NoError(t, err)
	_, err = p.ApplyChanges(t.Context(), syncAccount, complete100, "", first, 0)
	require.NoError(t, err)

	provider.mailboxes = append(pollMailboxes, Mailbox{ProviderID: "Label_new", Name: "New"})
	second, err := p.ReadChanges(t.Context(), syncAccount, "100", first.NextPageToken)
	require.NoError(t, err)
	_, err = p.ApplyChanges(t.Context(), syncAccount, complete100, first.NextPageToken, second, 0)

	require.NoError(t, err)
	assert.Equal(t, []string{"Label_new"}, storage.stored("moved").message.MailboxProviderIDs)
}

// A recorded update naming a mailbox deleted since is applied without it,
// so the poll goes on to later pages.
func TestPollDeletedMailbox(t *testing.T) {
	provider := &fakeProvider{
		mailboxes: pollMailboxes,
		changes: []ChangePage{{
			Update:   []MessageUpdate{{ProviderID: "moved", MailboxProviderIDs: []string{"INBOX", "Label_deleted"}}},
			Position: "200",
		}},
	}
	p, storage := newPollTest(provider, inboxMessage("moved", syncNow.Add(-time.Hour)))

	runPoll(t, p, "100")

	assert.Equal(t, []string{"INBOX"}, storage.stored("moved").message.MailboxProviderIDs)
	assert.Equal(t, "200", storage.current().Position)
}

// A position the provider no longer keeps changes for ends the poll for a
// full sync.
func TestPollExpired(t *testing.T) {
	provider := &fakeProvider{changesErr: fmt.Errorf("%w: gmail api: 404", ErrPositionExpired)}
	p, _ := newPollTest(provider)

	_, err := p.ReadChanges(t.Context(), syncAccount, "100", "")

	require.ErrorIs(t, err, ErrPositionExpired)
	assert.Equal(t, EndingResync, EndingOf(err))
}

// A failed poll records its failure and keeps the account complete.
func TestPollFail(t *testing.T) {
	p, storage := newPollTest(&fakeProvider{})
	storage.state.Failure = ""
	storage.state.FailedAt = nil

	require.NoError(t, p.Fail(t.Context(), syncAccount, "100", FailureProvider))

	state := storage.current()
	assert.Equal(t, StatusComplete, state.Status)
	assert.Equal(t, FailureProvider, state.Failure)
	require.NotNil(t, state.FailedAt)
	assert.Equal(t, syncNow, *state.FailedAt)
}

// A failure for a poll superseded, or an account gone, records nothing.
func TestPollFailEnded(t *testing.T) {
	p, storage := newPollTest(&fakeProvider{})
	storage.state.Failure = ""
	storage.state.FailedAt = nil

	require.NoError(t, p.Fail(t.Context(), syncAccount, "50", FailureProvider))
	assert.Empty(t, storage.current().Failure)

	storage.gone = true
	require.NoError(t, p.Fail(t.Context(), syncAccount, "100", FailureProvider))
}

// A poll inside a running full sync writes while the account runs at the
// run's generation and position: its saves take that generation, so the
// sweep keeps them, and its last part moves only the position, leaving the
// time and any failure to the full sync's finish.
func TestPollDuringCopy(t *testing.T) {
	provider := &fakeProvider{
		mailboxes: pollMailboxes,
		messages:  map[string]Message{"arrived": inboxMessage("arrived", syncNow)},
		changes:   []ChangePage{{Fetch: []string{"arrived"}, Position: "150"}},
	}
	storage := newFakeStorage(SyncState{Status: StatusRunning, Generation: 2, Position: "100", Failure: FailureProvider})
	storage.mailboxes = pollMailboxes
	p := NewPoll(storage, provider, fakeAccess{}, func() time.Time { return syncNow })
	from := PollFrom{Scope: PollDuringCopy, Position: "100", Generation: 2}
	page, err := p.ReadChanges(t.Context(), syncAccount, from.Position, "")
	require.NoError(t, err)

	_, err = p.ApplyChanges(t.Context(), syncAccount, from, "", page, 0)

	require.NoError(t, err)
	assert.Equal(t, 2, storage.stored("arrived").generation)
	state := storage.current()
	assert.Equal(t, "150", state.Position)
	assert.Equal(t, StatusRunning, state.Status)
	assert.Nil(t, state.LastSyncedAt)
	assert.Equal(t, FailureProvider, state.Failure, "the full sync's finish clears it")
}

// A poll writes only in the account state its scope expects.
func TestPollScopes(t *testing.T) {
	for name, tc := range map[string]struct {
		from  PollFrom
		state SyncState
	}{
		"during a copy, the account complete":  {from: PollFrom{Scope: PollDuringCopy, Position: "100", Generation: 2}, state: SyncState{Status: StatusComplete, Generation: 2, Position: "100"}},
		"during a copy, a newer full sync":     {from: PollFrom{Scope: PollDuringCopy, Position: "100", Generation: 2}, state: SyncState{Status: StatusRunning, Generation: 3, Position: "100"}},
		"during a copy, the position moved":    {from: PollFrom{Scope: PollDuringCopy, Position: "100", Generation: 2}, state: SyncState{Status: StatusRunning, Generation: 2, Position: "150"}},
		"complete, the account running a copy": {from: complete100, state: SyncState{Status: StatusRunning, Generation: 2, Position: "100"}},
	} {
		t.Run(name, func(t *testing.T) {
			storage := newFakeStorage(tc.state)
			p := NewPoll(storage, &fakeProvider{mailboxes: pollMailboxes}, fakeAccess{}, func() time.Time { return syncNow })

			_, err := p.ApplyChanges(t.Context(), syncAccount, tc.from, "", ChangePage{Position: "200"}, 0)

			require.ErrorIs(t, err, ErrSuperseded)
			assert.Equal(t, tc.state.Position, storage.current().Position)
		})
	}
}

// The last part of a poll that committed before its step was recorded runs
// again on a resumed run and succeeds, in either scope, rather than ending
// the sync as superseded.
func TestPollLastPartAgain(t *testing.T) {
	for name, tc := range map[string]struct {
		from  PollFrom
		state SyncState
	}{
		"complete":    {from: complete100, state: SyncState{Status: StatusComplete, Generation: 1, Position: "100"}},
		"during copy": {from: PollFrom{Scope: PollDuringCopy, Position: "100", Generation: 2}, state: SyncState{Status: StatusRunning, Generation: 2, Position: "100"}},
	} {
		t.Run(name, func(t *testing.T) {
			provider := &fakeProvider{
				mailboxes: pollMailboxes,
				messages:  map[string]Message{"arrived": inboxMessage("arrived", syncNow)},
				changes:   []ChangePage{{Fetch: []string{"arrived"}, Position: "150"}},
			}
			storage := newFakeStorage(tc.state)
			storage.mailboxes = pollMailboxes
			p := NewPoll(storage, provider, fakeAccess{}, func() time.Time { return syncNow })
			page, err := p.ReadChanges(t.Context(), syncAccount, tc.from.Position, "")
			require.NoError(t, err)
			_, err = p.ApplyChanges(t.Context(), syncAccount, tc.from, "", page, 0)
			require.NoError(t, err)

			_, err = p.ApplyChanges(t.Context(), syncAccount, tc.from, "", page, 0)

			require.NoError(t, err)
			assert.Equal(t, "150", storage.current().Position)
			assert.Equal(t, []string{"arrived"}, storage.saved())
		})
	}
}

// Only the last part accepts its own position already written: an earlier
// part finding the position moved was superseded.
func TestPollEarlierPartMoved(t *testing.T) {
	storage := newFakeStorage(SyncState{Status: StatusComplete, Generation: 1, Position: "150"})
	page := ChangePage{Deleted: []string{"x"}, Fetch: []string{"y"}, Position: "150"}
	p := NewPoll(storage, &fakeProvider{mailboxes: pollMailboxes}, fakeAccess{}, func() time.Time { return syncNow })

	_, err := p.ApplyChanges(t.Context(), syncAccount, complete100, "next", page, 0)

	require.ErrorIs(t, err, ErrSuperseded)
}

// A message a poll during the copy deletes stays gone through the sweep and
// the finish, whether the copy reached it already or had yet to.
func TestFullSyncPollDeletes(t *testing.T) {
	// Given a re-run copying, which has saved one message, with another
	// from the generation before not reached yet.
	storage := newFakeStorage(SyncState{Status: StatusRunning, Generation: 2, Position: "100"})
	storage.mailboxes = pollMailboxes
	storage.messages["copied"] = storedMessage{message: inboxMessage("copied", syncNow), generation: 2}
	storage.messages["not-reached"] = storedMessage{message: inboxMessage("not-reached", syncNow.Add(-time.Hour)), generation: 1}
	storage.messages["kept"] = storedMessage{message: inboxMessage("kept", syncNow), generation: 2}
	provider := &fakeProvider{mailboxes: pollMailboxes, changes: []ChangePage{{Deleted: []string{"copied", "not-reached"}, Position: "150"}}}
	p := NewPoll(storage, provider, fakeAccess{}, func() time.Time { return syncNow })
	s := NewFullSync(storage, provider, fakeAccess{}, func() time.Time { return syncNow })
	run := Run{Generation: 2, Position: "100"}
	from := PollFrom{Scope: PollDuringCopy, Position: "100", Generation: 2}

	// When the run polls, then sweeps and finishes.
	page, err := p.ReadChanges(t.Context(), syncAccount, from.Position, "")
	require.NoError(t, err)
	_, err = p.ApplyChanges(t.Context(), syncAccount, from, "", page, 0)
	require.NoError(t, err)
	for more := true; more; {
		more, err = s.DeleteEarlier(t.Context(), syncAccount, run)
		require.NoError(t, err)
	}
	require.NoError(t, s.Finish(t.Context(), syncAccount, run))

	// Then both are gone and the rest kept.
	assert.Equal(t, []string{"kept"}, storage.saved())
	assert.Equal(t, "150", storage.current().Position)
}
