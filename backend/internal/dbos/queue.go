package dbos

import "github.com/dbos-inc/dbos-transact-golang/dbos"

// Queues group workflows that share a limit. Enqueuers name them, so they
// are fixed like the workflows' names.
const (
	// emailQueue runs every workflow that sends inbok's own email, so one
	// limit on it, e.g. sends at once, protects the SMTP account.
	emailQueue = "inbok.email"

	// googleQueue runs the workflows that call Google for background work,
	// e.g. revoking a disconnected account's token.
	googleQueue = "inbok.google"

	// googleSendQueue runs the workflows that send members' messages through
	// Gmail. A run keeps its place in a queue's limit until it ends, and
	// priority orders only waiting runs, so a member's send would wait
	// behind long runs on googleQueue were it there (ADR004).
	googleSendQueue = "inbok.google-send"

	// contentQueue runs the workflows that delete message content from
	// the content store, which call no provider.
	contentQueue = "inbok.content"
)

// registerQueue registers the queue name, keeping its saved settings if it
// exists. It returns DBOS's error saving it.
func (w *Worker) registerQueue(name string) error {
	// Its limits can be changed while DBOS runs, and DBOS's default would
	// reset them to the options here, none, at every start.
	_, err := dbos.RegisterQueue(w.ctx, name, dbos.WithQueueOnConflict(dbos.QueueConflictNeverUpdate))
	return err
}
