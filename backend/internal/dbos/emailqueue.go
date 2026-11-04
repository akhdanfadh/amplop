package dbos

import "github.com/dbos-inc/dbos-transact-golang/dbos"

// emailQueue runs every workflow that sends inbok's own email, so one limit
// on it, e.g. sends at once, protects the SMTP account. Enqueuers name it,
// so it is fixed like the workflows' names.
const emailQueue = "inbok.email"

// registerEmailQueue registers [emailQueue], keeping its saved settings if
// it exists. It returns DBOS's error saving it.
func (w *Worker) registerEmailQueue() error {
	// Its limits can be changed while DBOS runs, and DBOS's default would
	// reset them to the options here, none, at every start.
	_, err := dbos.RegisterQueue(w.ctx, emailQueue, dbos.WithQueueOnConflict(dbos.QueueConflictNeverUpdate))
	return err
}
