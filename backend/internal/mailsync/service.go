package mailsync

import (
	"context"
	"errors"
	"time"

	"inbok/internal/attemptlimit"
	"inbok/internal/connectedaccount"
)

const (
	// requestWindow is how often a member may start an account's sync by
	// hand, inbok's choice: one sync runs per account at a time, but a
	// poll that finds nothing ends in about a second, so without it a
	// script could start a run, and leave its records, every second; a
	// minute would refuse a member's second click while waiting for a
	// mail (ADR001, "Polling").
	requestWindow = 15 * time.Second
	// stoppedAfter is how long without a successful run of the minute's
	// schedule means syncing has stopped, inbok's choice: five missed
	// minutes, so a worker's restart or one failed run shows nothing.
	stoppedAfter = 5 * time.Minute
)

// requestLimit allows one request to sync an account per [requestWindow].
var requestLimit = attemptlimit.Limit{Window: requestWindow, Max: 1}

// ErrRequestThrottled means the account's sync was requested moments ago.
var ErrRequestThrottled = errors.New("the account's sync was requested moments ago")

// Report is where an account's synchronization stands, as its member sees
// it.
type Report struct {
	AccountID int64
	Status    Status
	// MessagesSoFar counts the messages a running full sync has saved; 0
	// unless [Report.Status] is [StatusRunning].
	MessagesSoFar int
	// Failure, FailedAt, and LastSyncedAt are the [SyncState]'s.
	Failure      Failure
	FailedAt     *time.Time
	LastSyncedAt *time.Time
	// RequestedAt is the server's time of the member's last request to
	// sync the account now; nil before the first. Only it and the limit
	// read it: its value, not a clock, says a newer request came.
	RequestedAt *time.Time
}

// SyncRequest is what a request to sync an account now finds, locked.
type SyncRequest struct {
	// NeedsReconnect is whether the provider stopped accepting the
	// account's permission.
	NeedsReconnect bool
	// RequestedAt is [Report.RequestedAt].
	RequestedAt *time.Time
}

// scheduleRuns says whether the minute's sync schedule runs, e.g. the DBOS
// client.
type scheduleRuns interface {
	// SyncScheduleSucceeded reports whether a run of the sync schedule
	// created after since succeeded.
	SyncScheduleSucceeded(ctx context.Context, since time.Time) (bool, error)
}

// Service reports where members' accounts' synchronization stands, and
// starts an account's sync when its member asks.
type Service struct {
	storage Storage
	runs    scheduleRuns
	now     func() time.Time
}

// NewService returns a [Service] given storage, the schedule's runs, and a
// clock.
func NewService(storage Storage, runs scheduleRuns, now func() time.Time) *Service {
	return &Service{storage: storage, runs: runs, now: now}
}

// Reports returns the reports of those of accountIDs that have a sync
// state, in account ID order. The caller checks that the accounts are the
// member's.
func (s *Service) Reports(ctx context.Context, accountIDs []int64) ([]Report, error) {
	return s.storage.Reports(ctx, accountIDs)
}

// Stopped reports whether syncing has stopped on this inbok: no run of the
// minute's schedule succeeded within stoppedAfter, e.g. as no worker runs,
// or it runs without a Google client (ADR001, "Polling").
func (s *Service) Stopped(ctx context.Context) (bool, error) {
	succeeded, err := s.runs.SyncScheduleSucceeded(ctx, s.now().Add(-stoppedAfter))
	if err != nil {
		return false, err
	}
	return !succeeded, nil
}

// Request starts the sync of userID's account accountID now, unless one is
// already queued or running, which keeps its place, and returns the
// account's report. It returns [ErrNotFound] when userID has no account
// accountID, [connectedaccount.ErrNeedsReconnect] when it needs
// reconnecting, and an [*attemptlimit.Error] wrapping
// [ErrRequestThrottled] when its sync was requested within requestWindow.
//
// DESIGN: A poll that finishes polls once more when a request came since
// its first read (see [Poll.Recheck]), so a read of the provider starts
// after the request, and its mail shows within seconds. The page does not
// track when a request is answered, which would take exact bookkeeping
// across the server's and the worker's clocks (ADR001, "Polling"). A
// request that comes after a run's last look, while the run still ends,
// finds the run and starts none: the next minute's check then starts its
// poll, as without the request.
func (s *Service) Request(ctx context.Context, userID, accountID int64) (Report, error) {
	return s.storage.RequestSync(ctx, userID, accountID, func(request SyncRequest) (time.Time, error) {
		if request.NeedsReconnect {
			return time.Time{}, connectedaccount.ErrNeedsReconnect
		}
		now := s.now().UTC()
		var requested []time.Time
		if request.RequestedAt != nil {
			requested = append(requested, *request.RequestedAt)
		}
		if wait := attemptlimit.Wait(requested, now, requestLimit); wait > 0 {
			return time.Time{}, &attemptlimit.Error{Err: ErrRequestThrottled, Wait: wait}
		}
		return now, nil
	})
}
