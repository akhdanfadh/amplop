package dbos

import "github.com/dbos-inc/dbos-transact-golang/dbos"

// Queues group workflows that share a limit. Enqueuers name them, so they
// are fixed like the workflows' names.
const (
	// emailQueue runs every workflow that sends inbok's own email, so one
	// limit on it, e.g. sends at once, protects the SMTP account.
	emailQueue = "inbok.email"

	// googleQueue runs every workflow that calls Google, so one limit on it
	// protects the Google project's quota.
	googleQueue = "inbok.google"
)

// registerQueue registers the queue name, keeping its saved settings if it
// exists. It returns DBOS's error saving it.
func (w *Worker) registerQueue(name string) error {
	// Its limits can be changed while DBOS runs, and DBOS's default would
	// reset them to the options here, none, at every start.
	_, err := dbos.RegisterQueue(w.ctx, name, dbos.WithQueueOnConflict(dbos.QueueConflictNeverUpdate))
	return err
}
