package mailsync

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

const (
	// failedRetryAfter is how long after a failed full sync the schedule
	// starts another (ADR001, "Full sync").
	failedRetryAfter = time.Hour
	// checkConcurrency bounds the position checks in flight at once, inbok's
	// choice: enough to check an installation's accounts in seconds, few
	// enough to stay a small share of the provider's requests.
	checkConcurrency = 10
	// checkTimeout bounds one account's check, inbok's choice, so one slow
	// account cannot hold the schedule; a check that times out starts a
	// sync, whose own retries take over.
	checkTimeout = 20 * time.Second
	// checkBudget bounds all of one run's checks, inbok's choice: under the
	// minute between runs, so runs do not overlap and checkConcurrency
	// holds. Accounts not checked in time wait for the next run, which
	// starts at another account.
	checkBudget = 40 * time.Second
)

// errStopped means an account's provider work stopped while it was
// checked.
var errStopped = errors.New("account's provider work stopped")

// Schedule picks the accounts whose sync run should start now, for a
// workflow that runs it every minute (ADR001, "Polling").
type Schedule struct {
	storage  Storage
	provider Provider
	access   accountAccess
	now      func() time.Time
	// budget and timeout are checkBudget and checkTimeout, which tests
	// shorten.
	budget  time.Duration
	timeout time.Duration
}

// NewSchedule returns a [Schedule] given storage, the provider it asks,
// access to its accounts' tokens, and a clock.
func NewSchedule(storage Storage, provider Provider, access accountAccess, now func() time.Time) *Schedule {
	return &Schedule{storage: storage, provider: provider, access: access, now: now, budget: checkBudget, timeout: checkTimeout}
}

// Due returns, in ID order, the accounts whose provider work may run and
// whose sync run should start: those waiting for a full sync, those whose
// full sync failed failedRetryAfter ago or longer, and those complete whose
// position at the provider moved, or that have a failure recorded, which a
// poll clears. A complete account whose position could not be read starts
// one too, unless a failure is already recorded for it, so the poll's
// retries decide what to record, once. A check that a stopped account
// refuses starts nothing, and complete accounts not checked within
// checkBudget wait for the next run. A complete account with no failure
// whose position did not move is recorded as synced when the provider
// answered (ADR001, "Polling"), after every check.
func (s *Schedule) Due(ctx context.Context) ([]int64, error) {
	now := s.now()
	due, err := s.storage.DueSyncs(ctx, now.Add(-failedRetryAfter))
	if err != nil {
		return nil, err
	}

	var (
		mu        sync.Mutex
		started   []int64
		confirmed []Confirmed
		complete  []DueSync
		wg        sync.WaitGroup
	)
	start := func(accountID int64) {
		mu.Lock()
		defer mu.Unlock()
		started = append(started, accountID)
	}
	confirm := func(c Confirmed) {
		mu.Lock()
		defer mu.Unlock()
		confirmed = append(confirmed, c)
	}
	for _, account := range due {
		if account.Status == StatusComplete {
			complete = append(complete, account)
			continue
		}
		start(account.AccountID)
	}

	complete = rotated(complete, now)
	budget, cancel := context.WithTimeout(ctx, s.budget)
	defer cancel()
	slots := make(chan struct{}, checkConcurrency)
	for _, account := range complete {
		select {
		case slots <- struct{}{}:
		case <-budget.Done():
		}
		if budget.Err() != nil {
			break
		}
		wg.Go(func() {
			defer func() { <-slots }()
			switch checked := s.check(budget, account); checked.outcome {
			case checkMoved:
				start(account.AccountID)
			case checkQuiet:
				confirm(Confirmed{AccountID: account.AccountID, Position: account.Position, At: checked.at})
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(confirmed) > 0 {
		if err := s.storage.ConfirmSynced(ctx, confirmed); err != nil {
			return nil, err
		}
	}
	slices.Sort(started)
	return started, nil
}

// rotated returns accounts starting at the one the minute of now picks,
// so each run starts its checks at another account and the ones a short
// budget leaves out differ from run to run.
func rotated(accounts []DueSync, now time.Time) []DueSync {
	if len(accounts) == 0 {
		return accounts
	}
	offset := int(now.Unix()/60) % len(accounts)
	return append(slices.Clone(accounts[offset:]), accounts[:offset]...)
}

// checkOutcome is what one complete account's check found.
type checkOutcome int

const (
	// checkUnknown means the check says nothing of the account: it
	// stopped, or the run's budget cut the check short.
	checkUnknown checkOutcome = iota + 1
	// checkMoved means the account's sync should start.
	checkMoved
	// checkQuiet means the account is synced: its position did not move
	// and no failure is recorded.
	checkQuiet
)

// checked is a check's outcome, and for checkQuiet when the provider
// answered.
type checked struct {
	outcome checkOutcome
	at      time.Time
}

// check returns what complete account's check finds, within checkTimeout:
// checkMoved when its position at the provider differs from the stored one
// or a failure is recorded, or when the position could not be read while no
// failure is recorded, and checkQuiet when the position did not move and no
// failure is recorded. ctx carries the run's budget.
func (s *Schedule) check(ctx context.Context, account DueSync) checked {
	budget := ctx
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	position, err := s.position(ctx, account.AccountID)
	switch {
	case err == nil && (position != account.Position || account.Failure != ""):
		return checked{outcome: checkMoved}
	case err == nil:
		return checked{outcome: checkQuiet, at: s.now().UTC()}
	case errors.Is(err, errStopped), budget.Err() != nil:
		// A check the run's budget cut short says nothing of the account.
		return checked{outcome: checkUnknown}
	case account.Failure == "":
		return checked{outcome: checkMoved}
	default:
		return checked{outcome: checkUnknown}
	}
}

// position returns where account accountID stands at the provider now, or
// an error wrapping errStopped when its provider work stopped.
func (s *Schedule) position(ctx context.Context, accountID int64) (string, error) {
	token, err := s.access.Token(ctx, accountID)
	if err != nil {
		if ending := EndingOf(err); ending == EndingWait || ending == EndingGone {
			return "", errors.Join(errStopped, err)
		}
		return "", err
	}
	position, err := s.provider.Position(ctx, token)
	if err != nil {
		return "", providerFailed("read position", err)
	}
	return position, nil
}
