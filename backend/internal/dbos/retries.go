package dbos

import (
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
)

// retries is how DBOS retries a step that returns an error: up to `attempts`
// more times, waiting first `base`, then twice as long each time, at most
// `ceiling`. DBOS varies each wait by up to 5% either way.
type retries struct {
	attempts int
	base     time.Duration
	ceiling  time.Duration
}

// options returns r as DBOS step options.
func (r retries) options() []dbos.StepOption {
	return []dbos.StepOption{
		dbos.WithStepMaxRetries(r.attempts),
		dbos.WithStepBaseInterval(r.base),
		dbos.WithStepBackoffFactor(2),
		dbos.WithStepMaxInterval(r.ceiling),
	}
}

// NOTE: DBOS counts a step's retries, and sleeps between them, in memory. A
// worker restarted during them runs the step again with a fresh count, so
// every policy holds for each run of a worker, not across restarts.
