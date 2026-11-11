package postgres

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB stores inbok's data in PostgreSQL. Each feature adds its methods.
type DB struct {
	pool      *pgxpool.Pool
	workflows Workflows // nil until [DB.WithWorkflows]
}

// Workflows saves background work in a transaction of [DB]'s, so it commits
// or rolls back with the change that needs it, e.g. internal/dbos's Client.
// It embeds one interface per feature that saves a job.
type Workflows interface {
	RecoveryWorkflows
	PasswordResetWorkflows
	ConnectedAccountWorkflows
	OutgoingMessageWorkflows
	SyncWorkflows
}

// OutgoingMessageWorkflows saves the jobs of outgoing messages.
type OutgoingMessageWorkflows interface {
	// EnqueueOutgoingMessage saves, in tx, the workflow that sends the
	// outgoing message messageID.
	EnqueueOutgoingMessage(ctx context.Context, tx pgx.Tx, messageID uuid.UUID) error
}

// SyncWorkflows saves the jobs of mail synchronization.
type SyncWorkflows interface {
	// EnqueueSync saves, in tx, the workflow that synchronizes account
	// accountID's mail with its provider, a full sync for a new account.
	// It reports false, saving nothing and leaving tx usable, when the
	// account's sync is already queued or running.
	EnqueueSync(ctx context.Context, tx pgx.Tx, accountID int64) (bool, error)
}

// ConnectedAccountWorkflows saves the jobs of connected accounts.
type ConnectedAccountWorkflows interface {
	// EnqueueAccountRemoval saves, in tx, the workflow that finishes the
	// account removal removalID: it revokes the kept refresh token at Google.
	EnqueueAccountRemoval(ctx context.Context, tx pgx.Tx, removalID int64) error
}

// PasswordResetWorkflows saves the jobs of password reset.
type PasswordResetWorkflows interface {
	// EnqueuePasswordResetLink saves, in tx, the workflow that sends a reset
	// link for the request with attemptID to the account with userID, if it
	// may reset; userID is 0 when no account has the requested email.
	EnqueuePasswordResetLink(ctx context.Context, tx pgx.Tx, attemptID, userID int64) error

	// EnqueuePasswordChangedNotice saves, in tx, the workflow that tells the
	// user with userID that their password changed at changedAt.
	EnqueuePasswordChangedNotice(ctx context.Context, tx pgx.Tx, userID int64, changedAt time.Time) error
}

// RecoveryWorkflows saves the jobs of administrator recovery.
type RecoveryWorkflows interface {
	// EnqueueRecoveryNotice saves, in tx, the workflow that emails every
	// active administrator about the recovery of the one with userID at
	// recoveredAt.
	EnqueueRecoveryNotice(ctx context.Context, tx pgx.Tx, userID int64, recoveredAt time.Time) error
}

// errNoWorkflows means a change that saves background work ran on a [DB]
// without [DB.WithWorkflows], and was rolled back.
var errNoWorkflows = errors.New("this change saves background work, which needs DB.WithWorkflows")

// WithWorkflows returns a DB sharing db's connections that saves background
// work with workflows. Closing either closes both.
func (db *DB) WithWorkflows(workflows Workflows) *DB {
	return &DB{pool: db.pool, workflows: workflows}
}

// defaultConnectTimeout bounds each new connection unless the URL sets
// connect_timeout. pgx has no default, so a silent host would hang it.
const defaultConnectTimeout = 5 * time.Second

// Open connects to url and checks that the database answers, so a wrong
// URL fails at startup instead of on the first request.
func Open(ctx context.Context, url string) (*DB, error) {
	cfg, err := poolConfig(url)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return &DB{pool: pool}, nil
}

// Close waits for borrowed connections to return, then closes them all.
func (db *DB) Close() {
	db.pool.Close()
}

// poolConfig parses url into the pool's settings.
func poolConfig(url string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}

	if cfg.ConnConfig.ConnectTimeout == 0 {
		cfg.ConnConfig.ConnectTimeout = defaultConnectTimeout
	}
	return cfg, nil
}
