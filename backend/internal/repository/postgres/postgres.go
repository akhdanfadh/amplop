package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB stores inbok's data in PostgreSQL. Each feature adds its methods.
type DB struct {
	pool      *pgxpool.Pool
	workflows Workflows // nil until WithWorkflows
}

// Workflows saves background work in a transaction of DB's, so it commits
// or rolls back with the change that needs it, e.g. internal/dbos's Client.
type Workflows interface {
	// EnqueueRecoveryNotice saves, in tx, the workflow that emails every active
	// administrator about the recovery of the one with userID at recoveredAt.
	EnqueueRecoveryNotice(ctx context.Context, tx pgx.Tx, userID int64, recoveredAt time.Time) error
}

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
