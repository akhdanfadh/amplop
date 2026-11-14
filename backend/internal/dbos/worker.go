// Package dbos runs inbok's background work on DBOS: the worker process
// opens DBOS on inbok's database, launches it, and shuts it down.
//
// DESIGN: Like smtp, this is a technology package named for what it wraps.
// Feature packages never import DBOS; this package registers their
// workflows and calls their plain methods as steps. The SDK's package is
// also named dbos, so code here reads dbos.X as the SDK.
package dbos

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5/pgxpool"

	"inbok/internal/logging"
)

const (
	// appName labels DBOS's database connections and recorded workflows.
	appName = "inbok"

	// applicationVersion is the version DBOS records on each workflow. A
	// worker resumes only workflows of its own version.
	//
	// DESIGN: DBOS's default is a hash of the binary, so each release would
	// leave the previous release's unfinished workflows behind. One fixed
	// version lets a new release resume them, which ADR015 already
	// requires workflows and their inputs to allow. A workflow whose steps
	// change must keep resuming old runs, e.g. with [dbos.Patch].
	applicationVersion = "1"

	// stageTimeout bounds each stage of DBOS's shutdown, e.g. waiting for
	// running workflows to unwind.
	stageTimeout = 3 * time.Second

	// shutdownBudget bounds all of [Worker.Shutdown]. With serve's 5-second limit
	// for the health listener it stays inside the worker's 15-second stop
	// grace period in deploy/compose.yaml.
	shutdownBudget = 7 * time.Second
)

// errShutdownBudget reports a DBOS shutdown that outlasted [shutdownBudget].
var errShutdownBudget = errors.New("DBOS did not shut down in time; unfinished workflows resume at the next start")

// overrides are DBOS's environment variables that replace the
// application version and executor ID set in its configuration.
var overrides = []string{"DBOS__APPVERSION", "DBOS__VMID"}

// Services holds the feature operations inbok's workflows call; cmd/inbok
// fills it, as it fills httpapi.Services. A nil field leaves its part of a
// workflow out, e.g. in tests.
type Services struct {
	EmailVerificationCleanup    expired
	RegistrationCleanup         expired
	PasswordResetCleanup        expired
	PasswordResetAttemptCleanup expired
	ConnectStateCleanup         expired
	RecoveryNotices             recoveryNotices
	PasswordResetLinks          passwordResetLinks
	PasswordChangedNotices      passwordChangedNotices
	AccountRemovals             accountRemovals
	OutgoingMessages            outgoingMessages
	FullSyncs                   fullSyncs
	Polls                       polls
	SyncSchedule                dueSyncs
	// MessageContent is the store the content deletions delete from.
	MessageContent        contentStore
	MessageContentCleanup expired
	// MessageBodies fetches messages' bodies in the background, for the
	// accounts the minute's body schedule finds them due.
	MessageBodies bodyFetches
	// SearchIndex keeps members' search indexes in step with their mail.
	SearchIndex searchIndex
}

// Worker runs inbok's workflows and schedules.
type Worker struct {
	ctx    dbos.Context
	logger *slog.Logger
}

// NewWorker opens DBOS on the database at url as the executor id, without
// migrating it, registers inbok's workflows calling services, and saves their
// schedules. It logs to logger. Only [Worker.Shutdown] stops it. It returns an
// error if the workflow tables are missing or older than the SDK needs, which
// inbok migrate fixes, if url is malformed, if DBOS__APPVERSION or DBOS__VMID
// is set, or if a schedule cannot be saved.
//
// DESIGN: DBOS resumes an executor's unfinished workflows only when an
// executor with the same id launches. A replaced or restarted worker keeps
// its id, so it resumes its predecessor's work; two workers running at
// once need different ids, or each would resume the other's.
func NewWorker(url, id string, services Services, logger *slog.Logger) (*Worker, error) {
	// 1. Refuse DBOS's own overrides: DBOS would silently resume other work
	// than applicationVersion and id select.
	for _, name := range overrides {
		if os.Getenv(name) != "" {
			return nil, fmt.Errorf("unset %s: inbok sets the application version and INBOK_WORKER_ID itself", name)
		}
	}

	// 2. Check the URL first: DBOS's error for a malformed URL quotes it,
	// password included; pgx's hides the password.
	if _, err := pgxpool.ParseConfig(url); err != nil {
		return nil, err
	}

	// 3. Create the DBOS context. Not a caller's context: DBOS records a
	// workflow cancelled by anything but its own Shutdown as CANCELLED,
	// which no worker resumes.
	dbosCtx, err := dbos.NewContext(context.Background(), dbos.Config{
		AppName:            appName,
		DatabaseURL:        url,
		ApplicationVersion: applicationVersion,
		ExecutorID:         id,
		// DBOS logs the URL, hiding only a password written before the
		// host.
		Logger:         logging.WithoutAttrs(logger, "database_url"),
		SkipMigrations: true,
	})
	if err != nil {
		return nil, err
	}

	// 4. Register every workflow and queue, which DBOS needs before Launch.
	w := &Worker{ctx: dbosCtx, logger: logger}
	if err := w.registerCleanup(w.cleanups(services)); err != nil {
		return nil, errors.Join(fmt.Errorf("schedule cleanup: %w", err), w.Shutdown())
	}
	if err := w.registerSyncSchedule(services.SyncSchedule); err != nil {
		return nil, errors.Join(fmt.Errorf("schedule sync: %w", err), w.Shutdown())
	}
	if err := w.registerMessageBodiesSchedule(services.MessageBodies); err != nil {
		return nil, errors.Join(fmt.Errorf("schedule message bodies: %w", err), w.Shutdown())
	}
	for _, queue := range []string{emailQueue, googleQueue, googleSendQueue, contentQueue} {
		if err := w.registerQueue(queue); err != nil {
			return nil, errors.Join(fmt.Errorf("register queue %s: %w", queue, err), w.Shutdown())
		}
	}
	if err := w.registerSearchQueue(); err != nil {
		return nil, errors.Join(fmt.Errorf("register queue %s: %w", searchQueue, err), w.Shutdown())
	}
	w.registerRecoveryNotice(services.RecoveryNotices)
	w.registerPasswordResetLink(services.PasswordResetLinks)
	w.registerPasswordChangedNotice(services.PasswordChangedNotices)
	w.registerAccountRemoval(services.AccountRemovals)
	w.registerContentDeletion(services.MessageContent)
	w.registerAccountContentDeletion(services.MessageContent)
	w.registerOutgoingMessage(services.OutgoingMessages)
	w.registerSync(services.FullSyncs, services.Polls)
	w.registerMessageBodies(services.MessageBodies)
	w.registerSearch(services.SearchIndex)

	// 5. Listen only to the queues whose workflows this worker runs. DBOS
	// listens to every queue by default and claims a run before finding its
	// workflow unregistered; the run then waits for this executor, so a
	// worker with Google settings could not take it.
	dbos.ListenQueues(w.ctx, listenedQueues(services)...)
	return w, nil
}

// listenedQueues returns the queues a worker calling services takes runs
// from: each Google queue, the content queue, and the search queue, only
// when it runs that queue's workflows.
func listenedQueues(services Services) []string {
	queues := []string{emailQueue}
	if services.AccountRemovals != nil || services.FullSyncs != nil || services.Polls != nil || services.MessageBodies != nil {
		queues = append(queues, googleQueue)
	}
	if services.OutgoingMessages != nil {
		queues = append(queues, googleSendQueue)
	}
	if services.MessageContent != nil {
		queues = append(queues, contentQueue)
	}
	if services.SearchIndex != nil {
		queues = append(queues, searchQueue)
	}
	return queues
}

// Launch resumes this worker's unfinished workflows, then starts running
// queued and scheduled work. It returns an error if DBOS cannot start.
func (w *Worker) Launch() error {
	return dbos.Launch(w.ctx)
}

// Shutdown stops new work and cancels running workflows, which stay pending and
// resume on the next [Worker.Launch] with the same id. It waits up to
// stageTimeout for each stage of DBOS's shutdown and up to shutdownBudget in
// all. It returns DBOS's error, or errShutdownBudget if DBOS is still shutting
// down; the caller then exits without waiting for it.
func (w *Worker) Shutdown() error {
	done := make(chan error, 1)
	go func() { done <- dbos.Shutdown(w.ctx, stageTimeout) }()

	select {
	case err := <-done:
		return err
	case <-time.After(shutdownBudget):
		return errShutdownBudget
	}
}
