package mailsync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/attemptlimit"
	"amplop/internal/connectedaccount"
)

// syncMember owns syncAccount in the service's tests.
const syncMember = 11

// fakeRuns answers whether the schedule succeeded, recording since.
type fakeRuns struct {
	succeeded bool
	err       error
	since     time.Time
}

func (f *fakeRuns) SyncScheduleSucceeded(_ context.Context, since time.Time) (bool, error) {
	f.since = since
	return f.succeeded, f.err
}

func newServiceTest(state SyncState) (*Service, *fakeStorage) {
	storage := newFakeStorage(state)
	storage.owner = syncMember
	return NewService(storage, &fakeRuns{succeeded: true}, func() time.Time { return syncNow }), storage
}

func TestServiceRequest(t *testing.T) {
	s, storage := newServiceTest(SyncState{Status: StatusComplete, Position: "100", LastSyncedAt: new(syncNow.Add(-time.Minute))})

	report, err := s.Request(t.Context(), syncMember, syncAccount)

	require.NoError(t, err)
	assert.Equal(t, Report{
		AccountID: syncAccount, Status: StatusComplete,
		LastSyncedAt: new(syncNow.Add(-time.Minute)), RequestedAt: &syncNow,
	}, report)
	assert.Equal(t, 1, storage.enqueued)
}

// One request per account fits every requestWindow; the refusal says how
// long to wait, and writes nothing.
func TestServiceRequestThrottled(t *testing.T) {
	for name, tc := range map[string]struct {
		ago  time.Duration
		wait time.Duration
	}{
		"just now":          {ago: time.Second, wait: requestWindow - time.Second},
		"at the window":     {ago: requestWindow},
		"before the window": {ago: requestWindow + time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			s, storage := newServiceTest(SyncState{Status: StatusComplete})
			earlier := syncNow.Add(-tc.ago)
			storage.requestedAt = &earlier

			_, err := s.Request(t.Context(), syncMember, syncAccount)

			if tc.wait == 0 {
				require.NoError(t, err)
				return
			}
			refusal, ok := errors.AsType[*attemptlimit.Error](err)
			require.True(t, ok, "got %v", err)
			require.ErrorIs(t, err, ErrRequestThrottled)
			assert.Equal(t, tc.wait, refusal.Wait)
			assert.Equal(t, &earlier, storage.requestedAt)
			assert.Zero(t, storage.enqueued)
		})
	}
}

// An account that needs reconnecting is refused, and one that is not the
// member's is not found.
func TestServiceRequestRefused(t *testing.T) {
	s, storage := newServiceTest(SyncState{Status: StatusComplete})
	storage.needsReconnect = true

	_, reconnectErr := s.Request(t.Context(), syncMember, syncAccount)
	_, otherErr := s.Request(t.Context(), syncMember+1, syncAccount)

	require.ErrorIs(t, reconnectErr, connectedaccount.ErrNeedsReconnect)
	require.ErrorIs(t, otherErr, ErrNotFound)
	assert.Nil(t, storage.requestedAt)
	assert.Zero(t, storage.enqueued)
}

func TestServiceStopped(t *testing.T) {
	for name, tc := range map[string]struct {
		succeeded bool
		stopped   bool
	}{
		"a run succeeded": {succeeded: true},
		"none did":        {stopped: true},
	} {
		t.Run(name, func(t *testing.T) {
			runs := &fakeRuns{succeeded: tc.succeeded}
			s := NewService(newFakeStorage(SyncState{}), runs, func() time.Time { return syncNow })

			stopped, err := s.Stopped(t.Context())

			require.NoError(t, err)
			assert.Equal(t, tc.stopped, stopped)
			assert.Equal(t, syncNow.Add(-stoppedAfter), runs.since)
		})
	}
}
