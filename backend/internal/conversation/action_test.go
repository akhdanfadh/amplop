package conversation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
)

// Every action has an opposite, whose opposite is the action again.
func TestActionOpposite(t *testing.T) {
	require.Len(t, opposites, len(Actions))
	for _, action := range Actions {
		assert.NotEqual(t, action, action.Opposite())
		assert.Equal(t, action, action.Opposite().Opposite())
	}
	assert.Empty(t, Action("delete").Opposite())
}

// Star and Mark unread change only the newest message; every other action,
// their opposites too, changes every message.
func TestActionOnNewest(t *testing.T) {
	var onNewest []Action
	for _, action := range Actions {
		if action.OnNewest() {
			onNewest = append(onNewest, action)
		}
	}
	assert.Equal(t, []Action{ActionUnread, ActionStar}, onNewest)
}

// The provider is handed each thread as storage listed it, with its newest
// message.
func TestApplyThread(t *testing.T) {
	thread := Thread{ID: idA, AccountID: 1, ProviderThreadID: "ta", ProviderNewestMessageID: "ma"}
	provider := &fakeProvider{}
	actions := newTestActions(&fakeActionStorage{threads: []Thread{thread}}, fakeAccess{}, provider)

	_, err := actions.Apply(t.Context(), 42, ActionStar, []uuid.UUID{idA})

	require.NoError(t, err)
	assert.Equal(t, []Thread{thread}, provider.threads)
}

type fakeActionStorage struct {
	mu      sync.Mutex
	threads []Thread
	listErr error
	// writeErr is returned for the account it names.
	writeErr map[int64]error
	readErr  error

	listed  []uuid.UUID
	written map[int64][]mailsync.MessageUpdate
	read    []uuid.UUID
}

func (f *fakeActionStorage) ListThreads(_ context.Context, _ int64, ids []uuid.UUID) ([]Thread, error) {
	f.listed = ids
	var found []Thread
	for _, thread := range f.threads {
		if slices.Contains(ids, thread.ID) {
			found = append(found, thread)
		}
	}
	return found, f.listErr
}

func (f *fakeActionStorage) UpdateMessages(ctx context.Context, accountID int64, updates []mailsync.MessageUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.writeErr[accountID]; err != nil {
		return err
	}
	if f.written == nil {
		f.written = map[int64][]mailsync.MessageUpdate{}
	}
	f.written[accountID] = append(f.written[accountID], updates...)
	return nil
}

func (f *fakeActionStorage) ReadConversations(ctx context.Context, _ int64, ids []uuid.UUID) ([]Conversation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.read = ids
	rows := make([]Conversation, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, Conversation{ID: id})
	}
	return rows, f.readErr
}

// fakeAccess hands out a token named after each account, or its error.
type fakeAccess struct {
	errs map[int64]error
}

func (f fakeAccess) Token(_ context.Context, accountID int64) (connectedaccount.AccessToken, error) {
	if err := f.errs[accountID]; err != nil {
		return connectedaccount.AccessToken{}, err
	}
	return connectedaccount.NewAccessToken(fmt.Sprint("token-", accountID)), nil
}

// fakeProvider answers each thread with one message of the thread's name,
// read, or with the thread's error.
type fakeProvider struct {
	mu   sync.Mutex
	errs map[string]error
	// block holds every call, or the threads it names, until its context
	// ends.
	block   bool
	blocked map[string]bool
	// hold keeps each call in flight until it is closed, when not nil.
	hold     chan struct{}
	calls    []string
	threads  []Thread
	inFlight int
	most     int
}

func (f *fakeProvider) ChangeThread(ctx context.Context, token connectedaccount.AccessToken, thread Thread, action Action) ([]mailsync.MessageUpdate, error) {
	threadID := thread.ProviderThreadID
	f.mu.Lock()
	f.calls = append(f.calls, fmt.Sprint(token.Value(), "/", threadID, "/", action))
	f.threads = append(f.threads, thread)
	f.inFlight++
	f.most = max(f.most, f.inFlight)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()
	if f.hold != nil {
		<-f.hold
	}
	if f.block || f.blocked[threadID] {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := f.errs[threadID]; err != nil {
		return nil, err
	}
	return []mailsync.MessageUpdate{{ProviderID: threadID + "-m", Seen: true}}, nil
}

func newTestActions(storage *fakeActionStorage, access fakeAccess, provider *fakeProvider) *ActionService {
	return NewActionService(storage, access, provider, slog.New(slog.DiscardHandler))
}

var (
	idA = uuid.MustParse("00000000-0000-4000-8000-00000000000a")
	idB = uuid.MustParse("00000000-0000-4000-8000-00000000000b")
	idC = uuid.MustParse("00000000-0000-4000-8000-00000000000c")
	idD = uuid.MustParse("00000000-0000-4000-8000-00000000000d")
)

// An action changes each account's threads with that account's token,
// writes what the provider answered per account, and returns the changed
// rows in the order asked, refusing an ID that is not the member's.
func TestApply(t *testing.T) {
	storage := &fakeActionStorage{threads: []Thread{
		{ID: idA, AccountID: 1, ProviderThreadID: "ta"},
		{ID: idB, AccountID: 2, ProviderThreadID: "tb"},
		{ID: idC, AccountID: 1, ProviderThreadID: "tc"},
	}}
	provider := &fakeProvider{}
	actions := newTestActions(storage, fakeAccess{}, provider)

	applied, err := actions.Apply(t.Context(), 42, ActionRead, []uuid.UUID{idC, idD, idB, idA, idC})

	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{idC, idD, idB, idA}, storage.listed, "each ID once")
	assert.ElementsMatch(t, []string{"token-1/ta/read", "token-2/tb/read", "token-1/tc/read"}, provider.calls)
	assert.ElementsMatch(t, []mailsync.MessageUpdate{{ProviderID: "ta-m", Seen: true}, {ProviderID: "tc-m", Seen: true}}, storage.written[1])
	assert.Equal(t, []mailsync.MessageUpdate{{ProviderID: "tb-m", Seen: true}}, storage.written[2])
	assert.Equal(t, []uuid.UUID{idC, idB, idA}, storage.read)
	assert.Equal(t, []Conversation{{ID: idC}, {ID: idB}, {ID: idA}}, applied.Conversations)
	assert.Equal(t, []Refusal{{ID: idD, Reason: ReasonNotFound}}, applied.Refused)
}

// A thread the provider refuses, or an account it cannot be reached for,
// is refused with why, while the rest change.
func TestApplyRefusals(t *testing.T) {
	for _, c := range []struct {
		name       string
		tokenErr   error
		threadErr  error
		writeErr   error
		wantReason Reason
	}{
		{"needs reconnect", connectedaccount.ErrNeedsReconnect, nil, nil, ReasonNeedsReconnect},
		{"unreadable token", connectedaccount.ErrTokenUnreadable, nil, nil, ReasonNeedsReconnect},
		{"account gone", connectedaccount.ErrNotFound, nil, nil, ReasonNotFound},
		{"member deactivated", connectedaccount.ErrDeactivated, nil, nil, ReasonNotFound},
		{"token refresh failed", fmt.Errorf("%w: down", connectedaccount.ErrRefreshFailed), nil, nil, ReasonFailed},
		{"credential changed", connectedaccount.ErrCredentialChanged, nil, nil, ReasonFailed},
		{"thread gone", nil, fmt.Errorf("%w: gmail api: 404", ErrNotFound), nil, ReasonNotFound},
		{"provider busy", nil, mailsync.ErrProviderBusy, nil, ReasonFailed},
		{"provider refused", nil, errors.New("gmail api: 400"), nil, ReasonFailed},
		{"sync state gone", nil, nil, mailsync.ErrNotFound, ReasonNotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage := &fakeActionStorage{
				threads:  []Thread{{ID: idA, AccountID: 1, ProviderThreadID: "ta"}, {ID: idB, AccountID: 2, ProviderThreadID: "tb"}},
				writeErr: map[int64]error{1: c.writeErr},
			}
			provider := &fakeProvider{errs: map[string]error{"ta": c.threadErr}}
			actions := newTestActions(storage, fakeAccess{errs: map[int64]error{1: c.tokenErr}}, provider)

			applied, err := actions.Apply(t.Context(), 42, ActionUnread, []uuid.UUID{idA, idB})

			require.NoError(t, err)
			assert.Equal(t, []Refusal{{ID: idA, Reason: c.wantReason}}, applied.Refused)
			assert.Equal(t, []Conversation{{ID: idB}}, applied.Conversations)
			assert.Empty(t, storage.written[1])
		})
	}
}

// A thread the provider has not changed when the budget runs out is
// refused as failed, and the action still answers.
func TestApplyBudget(t *testing.T) {
	budget := actionBudget
	actionBudget = 10 * time.Millisecond
	t.Cleanup(func() { actionBudget = budget })
	storage := &fakeActionStorage{threads: []Thread{{ID: idA, AccountID: 1, ProviderThreadID: "ta"}}}
	actions := newTestActions(storage, fakeAccess{}, &fakeProvider{block: true})

	applied, err := actions.Apply(t.Context(), 42, ActionUnread, []uuid.UUID{idA})

	require.NoError(t, err)
	assert.Equal(t, []Refusal{{ID: idA, Reason: ReasonFailed}}, applied.Refused)
	assert.Empty(t, applied.Conversations)
	assert.Empty(t, storage.written)
}

// A thread changed before the budget ran out is written and answered,
// with ctx, while one still waiting on the provider is refused.
func TestApplyAfterBudget(t *testing.T) {
	budget := actionBudget
	actionBudget = 20 * time.Millisecond
	t.Cleanup(func() { actionBudget = budget })
	storage := &fakeActionStorage{threads: []Thread{
		{ID: idA, AccountID: 1, ProviderThreadID: "ta"},
		{ID: idB, AccountID: 1, ProviderThreadID: "tb"},
	}}
	actions := newTestActions(storage, fakeAccess{}, &fakeProvider{blocked: map[string]bool{"tb": true}})

	applied, err := actions.Apply(t.Context(), 42, ActionRead, []uuid.UUID{idA, idB})

	require.NoError(t, err)
	assert.Equal(t, []Conversation{{ID: idA}}, applied.Conversations)
	assert.Equal(t, []Refusal{{ID: idB, Reason: ReasonFailed}}, applied.Refused)
	assert.Equal(t, []mailsync.MessageUpdate{{ProviderID: "ta-m", Seen: true}}, storage.written[1])
}

// The provider's budget ends early enough to keep the reserve of the
// caller's deadline for the writes and the answer.
func TestApplyReserve(t *testing.T) {
	reserve := actionReserve
	actionReserve = 50 * time.Millisecond
	t.Cleanup(func() { actionReserve = reserve })
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	storage := &fakeActionStorage{threads: []Thread{{ID: idA, AccountID: 1, ProviderThreadID: "ta"}}}
	actions := newTestActions(storage, fakeAccess{}, &fakeProvider{block: true})

	applied, err := actions.Apply(ctx, 42, ActionRead, []uuid.UUID{idA})

	require.NoError(t, err)
	assert.Equal(t, []Refusal{{ID: idA, Reason: ReasonFailed}}, applied.Refused)
}

// One account's threads reach the provider at most four at once.
func TestApplyConcurrency(t *testing.T) {
	storage := &fakeActionStorage{}
	ids := make([]uuid.UUID, 0, 9)
	for i := range 9 {
		thread := Thread{ID: uuid.New(), AccountID: 1, ProviderThreadID: fmt.Sprint("t", i)}
		storage.threads = append(storage.threads, thread)
		ids = append(ids, thread.ID)
	}
	provider := &fakeProvider{hold: make(chan struct{})}
	actions := newTestActions(storage, fakeAccess{}, provider)

	done := make(chan error)
	go func() {
		_, err := actions.Apply(t.Context(), 42, ActionRead, ids)
		done <- err
	}()
	require.Eventually(t, func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.inFlight == actionConcurrency
	}, time.Second, time.Millisecond)
	close(provider.hold)

	require.NoError(t, <-done)
	assert.Equal(t, actionConcurrency, provider.most)
	assert.Len(t, provider.calls, 9)
}

// Storage failing fails the whole action.
func TestApplyStorageFailure(t *testing.T) {
	broken := errors.New("database down")
	for name, storage := range map[string]*fakeActionStorage{
		"list":  {listErr: broken},
		"write": {threads: []Thread{{ID: idA, AccountID: 1, ProviderThreadID: "ta"}}, writeErr: map[int64]error{1: broken}},
		"read":  {threads: []Thread{{ID: idA, AccountID: 1, ProviderThreadID: "ta"}}, readErr: broken},
	} {
		t.Run(name, func(t *testing.T) {
			actions := newTestActions(storage, fakeAccess{}, &fakeProvider{})

			_, err := actions.Apply(t.Context(), 42, ActionUnread, []uuid.UUID{idA})

			require.ErrorIs(t, err, broken)
		})
	}
}

func TestApplyInvalid(t *testing.T) {
	tooMany := make([]uuid.UUID, MaxActionIDs+1)
	for i := range tooMany {
		tooMany[i] = uuid.New()
	}
	for _, c := range []struct {
		name   string
		action Action
		ids    []uuid.UUID
		want   error
	}{
		{"an unknown action", Action("delete"), []uuid.UUID{idA}, ErrInvalidAction},
		{"no IDs", ActionRead, nil, ErrInvalidIDs},
		{"too many IDs", ActionRead, tooMany, ErrInvalidIDs},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage := &fakeActionStorage{}
			provider := &fakeProvider{}
			actions := newTestActions(storage, fakeAccess{}, provider)

			_, err := actions.Apply(t.Context(), 42, c.action, c.ids)

			require.ErrorIs(t, err, c.want)
			assert.Nil(t, storage.listed)
			assert.Empty(t, provider.calls)
		})
	}
}
