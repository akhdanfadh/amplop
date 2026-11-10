package mailsync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
)

// syncNow is the fake clock's time.
var syncNow = time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)

// storedMessage is a message in [fakeStorage], with the generation that
// last saved it.
type storedMessage struct {
	message    Message
	generation int
}

// fakeStorage keeps one account's synchronized mail in memory, writing
// each change as [Storage.UpdateSyncedMail] documents.
type fakeStorage struct {
	mu        sync.Mutex
	gone      bool
	state     SyncState
	mailboxes []Mailbox
	messages  map[string]storedMessage
	// err, when set, fails every write.
	err error
}

func newFakeStorage(state SyncState) *fakeStorage {
	return &fakeStorage{state: state, messages: make(map[string]storedMessage)}
}

func (f *fakeStorage) UpdateSyncedMail(_ context.Context, accountID int64, update func(SyncState) (Change, error)) (Written, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return Written{}, f.err
	}
	if f.gone {
		return Written{}, ErrNotFound
	}
	change, err := update(f.state)
	if err != nil {
		return Written{}, err
	}

	mailboxes := f.mailboxes
	if change.Mailboxes != nil {
		mailboxes = change.Mailboxes
	}
	known := func(ids []string) bool {
		for _, id := range ids {
			if !slices.ContainsFunc(mailboxes, func(m Mailbox) bool { return m.ProviderID == id }) {
				return false
			}
		}
		return true
	}
	for _, message := range change.Saved {
		if !known(message.MailboxProviderIDs) {
			return Written{}, ErrUnknownMailbox
		}
	}
	for _, update := range change.Updated {
		if _, ok := f.messages[update.ProviderID]; ok && !known(update.MailboxProviderIDs) {
			return Written{}, ErrUnknownMailbox
		}
	}

	f.mailboxes = mailboxes
	for _, id := range change.Deleted {
		delete(f.messages, id)
	}
	for _, message := range change.Saved {
		f.messages[message.ProviderID] = storedMessage{message: message, generation: change.State.Generation}
	}
	for _, update := range change.Updated {
		stored, ok := f.messages[update.ProviderID]
		if !ok {
			continue
		}
		stored.message.MailboxProviderIDs = update.MailboxProviderIDs
		stored.message.Seen = update.Seen
		stored.message.Flagged = update.Flagged
		f.messages[update.ProviderID] = stored
	}

	var earlier []string
	for id, stored := range f.messages {
		if stored.generation < change.State.Generation {
			earlier = append(earlier, id)
		}
	}
	sort.Strings(earlier)
	deleted := min(change.DeleteEarlier, len(earlier))
	for _, id := range earlier[:deleted] {
		delete(f.messages, id)
	}

	f.state = change.State
	f.state.AccountID = accountID
	return Written{State: f.state, EarlierDeleted: deleted}, nil
}

func (f *fakeStorage) OldestMessage(context.Context, int64) (time.Time, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var oldest time.Time
	for _, stored := range f.messages {
		if oldest.IsZero() || stored.message.InternalDate.Before(oldest) {
			oldest = stored.message.InternalDate
		}
	}
	return oldest, !oldest.IsZero(), f.err
}

// stored returns the stored message with providerID.
func (f *fakeStorage) stored(providerID string) storedMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.messages[providerID]
}

// saved returns the stored messages' provider IDs, sorted.
func (f *fakeStorage) saved() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, 0, len(f.messages))
	for id := range f.messages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// current returns the stored sync state.
func (f *fakeStorage) current() SyncState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

// fakeProvider answers from fixed mailboxes, a position, listing pages,
// and messages, and records the calls it gets.
type fakeProvider struct {
	mailboxes []Mailbox
	position  string
	// pages are the listing's pages of provider IDs, newest first.
	pages    [][]string
	messages map[string]Message
	err      error

	mu      sync.Mutex
	calls   []string
	queries []ListQuery
}

func (f *fakeProvider) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeProvider) Mailboxes(context.Context, connectedaccount.AccessToken) ([]Mailbox, error) {
	f.record("mailboxes")
	return slices.Clone(f.mailboxes), f.err
}

func (f *fakeProvider) Position(context.Context, connectedaccount.AccessToken) (string, error) {
	f.record("position")
	return f.position, f.err
}

func (f *fakeProvider) ListMessages(_ context.Context, _ connectedaccount.AccessToken, query ListQuery) (MessagePage, error) {
	f.record("list")
	f.mu.Lock()
	f.queries = append(f.queries, query)
	f.mu.Unlock()
	if f.err != nil {
		return MessagePage{}, f.err
	}
	page := 0
	if query.PageToken != "" {
		_, err := fmt.Sscanf(query.PageToken, "page-%d", &page)
		if err != nil {
			return MessagePage{}, err
		}
	}
	listed := MessagePage{ProviderIDs: f.pages[page]}
	if page+1 < len(f.pages) {
		listed.NextPageToken = fmt.Sprintf("page-%d", page+1)
	}
	return listed, nil
}

func (f *fakeProvider) Messages(_ context.Context, _ connectedaccount.AccessToken, _ int64, providerIDs []string) (Fetched, error) {
	f.record("messages")
	if f.err != nil {
		return Fetched{}, f.err
	}
	var fetched Fetched
	for _, id := range providerIDs {
		message, ok := f.messages[id]
		if !ok {
			fetched.Gone = append(fetched.Gone, id)
			continue
		}
		fetched.Messages = append(fetched.Messages, message)
	}
	return fetched, nil
}

func (f *fakeProvider) Changes(context.Context, connectedaccount.AccessToken, string, string) (ChangePage, error) {
	return ChangePage{}, errors.New("a full sync reads no changes")
}

// fakeAccess hands out a token for every account, or fails with tokenErr,
// answers every check with checkErr, and counts the tokens it forgets.
type fakeAccess struct {
	tokenErr  error
	checkErr  error
	forgotten *int
}

func (f fakeAccess) Token(context.Context, int64) (connectedaccount.AccessToken, error) {
	if f.tokenErr != nil {
		return connectedaccount.AccessToken{}, f.tokenErr
	}
	return connectedaccount.NewAccessToken("access"), nil
}

func (f fakeAccess) Forget(int64) {
	if f.forgotten != nil {
		*f.forgotten++
	}
}

func (f fakeAccess) Check(context.Context, int64) error {
	return f.checkErr
}

// inboxMessage returns a message in INBOX received at date.
func inboxMessage(providerID string, date time.Time) Message {
	return Message{
		ProviderID: providerID, ThreadID: "t-" + providerID, InternalDate: date,
		Subject: "Subject " + providerID, MailboxProviderIDs: []string{"INBOX"},
	}
}

var syncMailboxes = []Mailbox{{ProviderID: "INBOX", Name: "INBOX", Role: RoleInbox}}

const syncAccount = 7

// runFullSync runs every step of a full sync of syncAccount in order, as the
// workflow does, and returns its run.
func runFullSync(t *testing.T, s *FullSync) Run {
	t.Helper()
	ctx := t.Context()
	run, err := s.Start(ctx, syncAccount)
	require.NoError(t, err)
	token := ""
	for page := 0; ; page++ {
		token, err = s.CopyPage(ctx, syncAccount, run, page, token)
		require.NoError(t, err)
		if token == "" {
			break
		}
	}
	for more := true; more; {
		more, err = s.DeleteEarlier(ctx, syncAccount, run)
		require.NoError(t, err)
	}
	require.NoError(t, s.Finish(ctx, syncAccount, run))
	return run
}

func TestFullSync(t *testing.T) {
	// Given an account whose last full sync failed and saved a message the
	// provider no longer lists, and a provider listing two pages: a message
	// at the window's edge, an older one, and one gone before its fetch.
	since := syncNow.Add(-window)
	failedAt := syncNow.Add(-time.Hour)
	storage := newFakeStorage(SyncState{Status: StatusFailed, Generation: 1, Failure: FailureProvider, FailedAt: &failedAt, PagesSynced: 3, MessagesSynced: 9})
	storage.messages["deleted-at-provider"] = storedMessage{message: inboxMessage("deleted-at-provider", syncNow), generation: 1}
	provider := &fakeProvider{
		mailboxes: syncMailboxes,
		position:  "4096",
		pages:     [][]string{{"new", "gone"}, {"edge", "old"}},
		messages: map[string]Message{
			"new":  inboxMessage("new", syncNow.Add(-time.Hour)),
			"edge": inboxMessage("edge", since),
			"old":  inboxMessage("old", since.Add(-time.Second)),
		},
	}
	s := NewFullSync(storage, provider, fakeAccess{}, func() time.Time { return syncNow })

	// When it runs.
	run := runFullSync(t, s)

	// Then only the message inside the window is kept, the rest are
	// deleted, and the state is complete at the position read before
	// listing, with the progress of both pages and no failure.
	assert.Equal(t, Run{Generation: 2, Since: since}, run)
	assert.Equal(t, []string{"new"}, storage.saved())
	assert.Equal(t, syncMailboxes, storage.mailboxes)
	assert.Equal(t, SyncState{
		AccountID: syncAccount, Status: StatusComplete, Position: "4096", Generation: 2,
		PagesSynced: 2, MessagesSynced: 1, LastSyncedAt: &syncNow,
	}, storage.current())
	assert.Equal(t, []string{"mailboxes", "position", "list", "messages", "list", "messages"}, provider.calls)
	for _, query := range provider.queries {
		assert.Equal(t, since, query.Since)
		assert.True(t, query.IncludeJunkAndTrash, "Spam and Trash are copied (ADR001)")
	}
}

// A page saved twice, e.g. by a step retried after its write, counts once.
func TestFullSyncPageRepeated(t *testing.T) {
	storage := newFakeStorage(SyncState{Status: StatusWaiting})
	provider := &fakeProvider{
		mailboxes: syncMailboxes,
		position:  "4096",
		pages:     [][]string{{"m1", "m2"}},
		messages:  map[string]Message{"m1": inboxMessage("m1", syncNow), "m2": inboxMessage("m2", syncNow)},
	}
	s := NewFullSync(storage, provider, fakeAccess{}, func() time.Time { return syncNow })
	run, err := s.Start(t.Context(), syncAccount)
	require.NoError(t, err)

	for range 2 {
		_, err := s.CopyPage(t.Context(), syncAccount, run, 0, "")
		require.NoError(t, err)
	}

	state := storage.current()
	assert.Equal(t, 1, state.PagesSynced)
	assert.Equal(t, 2, state.MessagesSynced)
}

// A full sync that a newer one superseded writes nothing more, and its end
// records nothing.
func TestFullSyncSuperseded(t *testing.T) {
	storage := newFakeStorage(SyncState{Status: StatusWaiting})
	provider := &fakeProvider{
		mailboxes: syncMailboxes,
		position:  "4096",
		pages:     [][]string{{"m1"}},
		messages:  map[string]Message{"m1": inboxMessage("m1", syncNow)},
	}
	s := NewFullSync(storage, provider, fakeAccess{}, func() time.Time { return syncNow })
	older, err := s.Start(t.Context(), syncAccount)
	require.NoError(t, err)
	_, err = s.Start(t.Context(), syncAccount)
	require.NoError(t, err)

	_, copyErr := s.CopyPage(t.Context(), syncAccount, older, 0, "")
	_, deleteErr := s.DeleteEarlier(t.Context(), syncAccount, older)
	finishErr := s.Finish(t.Context(), syncAccount, older)
	failErr := s.Fail(t.Context(), syncAccount, older, FailureInternal)

	require.ErrorIs(t, copyErr, ErrSuperseded)
	require.ErrorIs(t, deleteErr, ErrSuperseded)
	require.ErrorIs(t, finishErr, ErrSuperseded)
	require.NoError(t, failErr)
	assert.Equal(t, EndingGone, EndingOf(copyErr))
	assert.Empty(t, storage.saved())
	assert.Equal(t, StatusRunning, storage.current().Status)
	assert.Equal(t, 2, storage.current().Generation)
}

// A message in a mailbox made since the start is saved once the mailboxes
// are listed again.
func TestFullSyncNewMailbox(t *testing.T) {
	storage := newFakeStorage(SyncState{Status: StatusWaiting})
	labelled := inboxMessage("m1", syncNow)
	labelled.MailboxProviderIDs = []string{"INBOX", "Label_9"}
	provider := &fakeProvider{
		mailboxes: syncMailboxes,
		position:  "4096",
		pages:     [][]string{{"m1"}},
		messages:  map[string]Message{"m1": labelled},
	}
	s := NewFullSync(storage, provider, fakeAccess{}, func() time.Time { return syncNow })
	run, err := s.Start(t.Context(), syncAccount)
	require.NoError(t, err)
	label := Mailbox{ProviderID: "Label_9", Name: "Receipts"}
	provider.mailboxes = append(slices.Clone(syncMailboxes), label)

	_, err = s.CopyPage(t.Context(), syncAccount, run, 0, "")

	require.NoError(t, err)
	assert.Equal(t, []string{"m1"}, storage.saved())
	assert.Contains(t, storage.mailboxes, label)
}

// Earlier messages are deleted a batch at a time, and the last batch says
// none are left.
func TestFullSyncSweepBatches(t *testing.T) {
	storage := newFakeStorage(SyncState{Status: StatusRunning, Generation: 2})
	for i := range deleteBatch + 1 {
		id := fmt.Sprintf("m%05d", i)
		storage.messages[id] = storedMessage{message: inboxMessage(id, syncNow), generation: 1}
	}
	s := NewFullSync(storage, &fakeProvider{}, fakeAccess{}, func() time.Time { return syncNow })
	run := Run{Generation: 2}

	first, err := s.DeleteEarlier(t.Context(), syncAccount, run)
	require.NoError(t, err)
	afterFirst := len(storage.saved())
	second, err := s.DeleteEarlier(t.Context(), syncAccount, run)
	require.NoError(t, err)

	assert.True(t, first, "a full batch may leave more")
	assert.Equal(t, 1, afterFirst)
	assert.False(t, second)
	assert.Empty(t, storage.saved())
}

func TestFullSyncWait(t *testing.T) {
	storage := newFakeStorage(SyncState{Status: StatusRunning, Generation: 2, Position: "4096", PagesSynced: 1, MessagesSynced: 5})
	s := NewFullSync(storage, &fakeProvider{}, fakeAccess{}, func() time.Time { return syncNow })

	require.NoError(t, s.Wait(t.Context(), syncAccount, Run{Generation: 2}))

	assert.Equal(t, SyncState{AccountID: syncAccount, Status: StatusWaiting, Generation: 2, Position: "4096", PagesSynced: 1, MessagesSynced: 5}, storage.current())
}

func TestFullSyncFail(t *testing.T) {
	storage := newFakeStorage(SyncState{Status: StatusRunning, Generation: 2})
	s := NewFullSync(storage, &fakeProvider{}, fakeAccess{}, func() time.Time { return syncNow })

	require.NoError(t, s.Fail(t.Context(), syncAccount, Run{Generation: 2}, FailureProvider))

	assert.Equal(t, SyncState{AccountID: syncAccount, Status: StatusFailed, Generation: 2, Failure: FailureProvider, FailedAt: &syncNow}, storage.current())
}

// A full sync that ended before its start gave it a generation ends the
// account's state as it is, e.g. a complete one whose position expired.
func TestFullSyncEndBeforeStart(t *testing.T) {
	storage := newFakeStorage(SyncState{Status: StatusComplete, Generation: 1, Position: "4096"})
	s := NewFullSync(storage, &fakeProvider{}, fakeAccess{}, func() time.Time { return syncNow })

	require.NoError(t, s.Wait(t.Context(), syncAccount, Run{}))

	assert.Equal(t, StatusWaiting, storage.current().Status)
}

// Ending a full sync of a disconnected account records nothing and is no
// error.
func TestFullSyncEndGone(t *testing.T) {
	storage := newFakeStorage(SyncState{})
	storage.gone = true
	s := NewFullSync(storage, &fakeProvider{}, fakeAccess{}, func() time.Time { return syncNow })

	assert.NoError(t, s.Fail(t.Context(), syncAccount, Run{Generation: 1}, FailureInternal))
	assert.NoError(t, s.Wait(t.Context(), syncAccount, Run{Generation: 1}))
}

// Each step's error says how the full sync ends and what it records.
func TestFullSyncErrors(t *testing.T) {
	googleDown := errors.New("google unavailable")
	databaseDown := errors.New("database unavailable")
	for name, tc := range map[string]struct {
		tokens   error
		provider error
		storage  error
		ending   Ending
		failure  Failure
	}{
		"needs reconnecting":    {tokens: connectedaccount.ErrNeedsReconnect, ending: EndingWait, failure: FailureInternal},
		"deactivated":           {tokens: connectedaccount.ErrDeactivated, ending: EndingWait, failure: FailureInternal},
		"disconnected":          {tokens: connectedaccount.ErrNotFound, ending: EndingGone, failure: FailureInternal},
		"unreadable token":      {tokens: fmt.Errorf("%w: no key", connectedaccount.ErrTokenUnreadable), ending: EndingFail, failure: FailureTokenUnreadable},
		"token endpoint failed": {tokens: fmt.Errorf("%w: %w", connectedaccount.ErrRefreshFailed, googleDown), ending: EndingRetry, failure: FailureProvider},
		"token storage failed":  {tokens: databaseDown, ending: EndingRetry, failure: FailureInternal},
		"provider busy":         {provider: ErrProviderBusy, ending: EndingRetry, failure: FailureProvider},
		"provider refused":      {provider: googleDown, ending: EndingRetry, failure: FailureProvider},
		"token refused":         {provider: fmt.Errorf("%w: gmail api: 401", ErrUnauthorized), ending: EndingRetry, failure: FailureProvider},
		"storage failed":        {storage: databaseDown, ending: EndingRetry, failure: FailureInternal},
		"no sync state":         {storage: ErrNotFound, ending: EndingGone, failure: FailureInternal},
	} {
		t.Run(name, func(t *testing.T) {
			storage := newFakeStorage(SyncState{Status: StatusWaiting})
			storage.err = tc.storage
			provider := &fakeProvider{mailboxes: syncMailboxes, position: "4096", err: tc.provider}
			s := NewFullSync(storage, provider, fakeAccess{tokenErr: tc.tokens}, func() time.Time { return syncNow })

			_, err := s.Start(t.Context(), syncAccount)

			require.Error(t, err)
			assert.Equal(t, tc.ending, EndingOf(err))
			assert.Equal(t, tc.failure, FailureOf(err))
		})
	}
}

// A full sync that already ended, e.g. back to waiting, writes nothing
// more, though its generation is still the account's.
func TestFullSyncEnded(t *testing.T) {
	storage := newFakeStorage(SyncState{Status: StatusWaiting})
	provider := &fakeProvider{
		mailboxes: syncMailboxes,
		position:  "4096",
		pages:     [][]string{{"m1"}},
		messages:  map[string]Message{"m1": inboxMessage("m1", syncNow)},
	}
	s := NewFullSync(storage, provider, fakeAccess{}, func() time.Time { return syncNow })
	run, err := s.Start(t.Context(), syncAccount)
	require.NoError(t, err)
	require.NoError(t, s.Wait(t.Context(), syncAccount, run))

	_, err = s.CopyPage(t.Context(), syncAccount, run, 0, "")

	require.ErrorIs(t, err, ErrSuperseded)
	assert.Empty(t, storage.saved())
}

// A stopped account stops the steps that write only inbok's data too: a
// member deactivated after the last page leaves the earlier messages and
// the state as they were.
func TestFullSyncStoppedLate(t *testing.T) {
	for name, stop := range map[string]error{
		"deactivated":        connectedaccount.ErrDeactivated,
		"needs reconnecting": connectedaccount.ErrNeedsReconnect,
		"unreadable token":   connectedaccount.ErrTokenUnreadable,
	} {
		t.Run(name, func(t *testing.T) {
			storage := newFakeStorage(SyncState{Status: StatusRunning, Generation: 2})
			storage.messages["earlier"] = storedMessage{message: inboxMessage("earlier", syncNow), generation: 1}
			s := NewFullSync(storage, &fakeProvider{}, fakeAccess{checkErr: stop}, func() time.Time { return syncNow })
			run := Run{Generation: 2}

			_, deleteErr := s.DeleteEarlier(t.Context(), syncAccount, run)
			finishErr := s.Finish(t.Context(), syncAccount, run)

			require.ErrorIs(t, deleteErr, stop)
			require.ErrorIs(t, finishErr, stop)
			assert.Equal(t, EndingOf(stop), EndingOf(deleteErr))
			assert.Equal(t, []string{"earlier"}, storage.saved())
			assert.Equal(t, StatusRunning, storage.current().Status)
		})
	}
}

// A refused access token is forgotten, so the step's next try takes a new
// one.
func TestFullSyncUnauthorized(t *testing.T) {
	storage := newFakeStorage(SyncState{Status: StatusWaiting})
	provider := &fakeProvider{err: fmt.Errorf("%w: gmail api: 401", ErrUnauthorized)}
	forgotten := 0
	s := NewFullSync(storage, provider, fakeAccess{forgotten: &forgotten}, func() time.Time { return syncNow })

	_, err := s.Start(t.Context(), syncAccount)

	require.ErrorIs(t, err, ErrUnauthorized)
	assert.Equal(t, 1, forgotten)
}

// A full sync of an account whose copy holds mail older than the window
// reaches back to the oldest stored message: each is fetched again, so
// its state is current, and one the provider no longer has is deleted.
func TestFullSyncReachesOldest(t *testing.T) {
	oldest := syncNow.Add(-window - 30*24*time.Hour)
	storage := newFakeStorage(SyncState{Status: StatusComplete, Generation: 1, Position: "100"})
	for _, m := range []Message{inboxMessage("old", oldest), inboxMessage("old-gone", oldest.Add(time.Hour))} {
		storage.messages[m.ProviderID] = storedMessage{message: m, generation: 1}
	}
	read := inboxMessage("old", oldest)
	read.MailboxProviderIDs = nil
	read.Seen = true
	provider := &fakeProvider{
		mailboxes: syncMailboxes,
		pages:     [][]string{{"new", "old"}},
		messages:  map[string]Message{"new": inboxMessage("new", syncNow), "old": read},
	}
	s := NewFullSync(storage, provider, fakeAccess{}, func() time.Time { return syncNow })

	run := runFullSync(t, s)

	assert.Equal(t, oldest.Add(-time.Second), run.Since, "a second before the oldest, which pages keep after")
	assert.Equal(t, provider.queries[0].Since, run.Since)
	assert.Equal(t, []string{"new", "old"}, storage.saved())
	assert.Equal(t, read, storage.stored("old").message, "the old message's state is current")
}

// A copy holding only mail inside the window, or none, keeps the window.
func TestFullSyncWindow(t *testing.T) {
	storage := newFakeStorage(SyncState{Status: StatusComplete, Generation: 1})
	storage.messages["recent"] = storedMessage{message: inboxMessage("recent", syncNow.Add(-time.Hour)), generation: 1}
	s := NewFullSync(storage, &fakeProvider{mailboxes: syncMailboxes}, fakeAccess{}, func() time.Time { return syncNow })

	run, err := s.Start(t.Context(), syncAccount)

	require.NoError(t, err)
	assert.Equal(t, syncNow.Add(-window), run.Since)
}
