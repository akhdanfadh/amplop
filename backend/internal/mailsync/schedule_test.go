package mailsync

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
)

func TestScheduleDue(t *testing.T) {
	// Given accounts waiting, failed long enough ago, complete with a
	// position that moved, and complete with one that did not.
	storage := newFakeStorage(SyncState{})
	storage.due = []DueSync{
		{AccountID: 4, Status: StatusComplete, Position: "200"},
		{AccountID: 3, Status: StatusComplete, Position: "100"},
		{AccountID: 2, Status: StatusFailed, Failure: FailureProvider},
		{AccountID: 1, Status: StatusWaiting},
	}
	provider := &fakeProvider{positions: map[string]string{"access-3": "150", "access-4": "200"}}
	s := NewSchedule(storage, provider, fakeAccess{}, func() time.Time { return syncNow })

	due, err := s.Due(t.Context())

	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2, 3}, due)
	assert.Equal(t, syncNow.Add(-failedRetryAfter), storage.failedBefore)
}

// A check a stopped account refuses starts nothing; one that fails for
// another reason starts a sync, unless a failure is already recorded.
func TestScheduleCheckFails(t *testing.T) {
	refreshFailed := fmt.Errorf("%w: unavailable", connectedaccount.ErrRefreshFailed)
	storage := newFakeStorage(SyncState{})
	storage.due = []DueSync{
		{AccountID: 1, Status: StatusComplete, Position: "100"},
		{AccountID: 2, Status: StatusComplete, Position: "100"},
		{AccountID: 3, Status: StatusComplete, Position: "100"},
		{AccountID: 4, Status: StatusComplete, Position: "100", Failure: FailureProvider},
		{AccountID: 5, Status: StatusComplete, Position: "100"},
	}
	access := fakeAccess{tokenErrs: map[int64]error{
		1: connectedaccount.ErrDeactivated,
		2: connectedaccount.ErrNotFound,
		3: refreshFailed,
		4: refreshFailed,
		5: fmt.Errorf("%w: no key", connectedaccount.ErrTokenUnreadable),
	}}
	s := NewSchedule(storage, &fakeProvider{position: "100"}, access, func() time.Time { return syncNow })

	due, err := s.Due(t.Context())

	require.NoError(t, err)
	assert.Equal(t, []int64{3, 5}, due)
}

// A position read with a refused token forgets the token and starts the
// sync, whose retries take a new one.
func TestScheduleUnauthorized(t *testing.T) {
	storage := newFakeStorage(SyncState{})
	storage.due = []DueSync{{AccountID: 1, Status: StatusComplete, Position: "100"}}
	provider := &fakeProvider{positionErr: fmt.Errorf("%w: gmail api: 401", ErrUnauthorized)}
	forgotten := 0
	s := NewSchedule(storage, provider, fakeAccess{forgotten: &forgotten}, func() time.Time { return syncNow })

	due, err := s.Due(t.Context())

	require.NoError(t, err)
	assert.Equal(t, []int64{1}, due)
	assert.Equal(t, 1, forgotten)
}

// blockingProvider answers positions only once released, counting the
// checks in flight and the most at once.
type blockingProvider struct {
	fakeProvider
	release chan struct{}

	mu       sync.Mutex
	inFlight int
	most     int
}

func (b *blockingProvider) Position(ctx context.Context, token connectedaccount.AccessToken) (string, error) {
	b.mu.Lock()
	b.inFlight++
	b.most = max(b.most, b.inFlight)
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.inFlight--
		b.mu.Unlock()
	}()
	select {
	case <-b.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return b.fakeProvider.Position(ctx, token)
}

// completeAccounts returns n complete accounts at position 100.
func completeAccounts(n int) []DueSync {
	due := make([]DueSync, n)
	for i := range due {
		due[i] = DueSync{AccountID: int64(i + 1), Status: StatusComplete, Position: "100"}
	}
	return due
}

// Many accounts are checked at most checkConcurrency at a time, and every
// one is answered.
func TestScheduleConcurrency(t *testing.T) {
	storage := newFakeStorage(SyncState{})
	storage.due = completeAccounts(3 * checkConcurrency)
	provider := &blockingProvider{fakeProvider: fakeProvider{position: "101"}, release: make(chan struct{})}
	s := NewSchedule(storage, provider, fakeAccess{}, func() time.Time { return syncNow })
	go func() {
		// Let the checks pile up before releasing them one at a time.
		time.Sleep(50 * time.Millisecond)
		for range 3 * checkConcurrency {
			provider.release <- struct{}{}
		}
	}()

	due, err := s.Due(t.Context())

	require.NoError(t, err)
	assert.Len(t, due, 3*checkConcurrency)
	assert.Equal(t, checkConcurrency, provider.most)
}

// Checks not done within the run's budget start nothing.
func TestScheduleBudget(t *testing.T) {
	storage := newFakeStorage(SyncState{})
	storage.due = completeAccounts(2)
	provider := &blockingProvider{fakeProvider: fakeProvider{position: "101"}, release: make(chan struct{})}
	s := NewSchedule(storage, provider, fakeAccess{}, func() time.Time { return syncNow })
	s.budget = 50 * time.Millisecond

	due, err := s.Due(t.Context())

	require.NoError(t, err)
	assert.Empty(t, due, "a check the budget cut short says nothing of the account")
}

// Each minute's run starts its checks one account further on.
func TestScheduleRotation(t *testing.T) {
	accounts := completeAccounts(3)
	minute := syncNow.Truncate(time.Minute)
	first := func(at time.Time) int64 { return rotated(accounts, at)[0].AccountID }

	starts := []int64{first(minute), first(minute.Add(time.Minute)), first(minute.Add(2 * time.Minute)), first(minute.Add(3 * time.Minute))}

	assert.ElementsMatch(t, []int64{1, 2, 3}, starts[:3])
	assert.Equal(t, starts[0], starts[3])
	assert.Len(t, rotated(accounts, minute), 3)
	assert.Equal(t, completeAccounts(3), accounts, "the accounts given are left as they were")
}

// A failure recorded for a complete account starts a poll once its check
// succeeds, even with its position unchanged, so the poll clears it.
func TestScheduleRecovers(t *testing.T) {
	storage := newFakeStorage(SyncState{})
	storage.due = []DueSync{{AccountID: 1, Status: StatusComplete, Position: "100", Failure: FailureProvider}}
	s := NewSchedule(storage, &fakeProvider{position: "100"}, fakeAccess{}, func() time.Time { return syncNow })

	due, err := s.Due(t.Context())

	require.NoError(t, err)
	assert.Equal(t, []int64{1}, due)
}
