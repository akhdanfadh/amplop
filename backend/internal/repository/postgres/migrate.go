// Package postgres stores inbok's data in PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"inbok/internal/logging"
)

// migrations holds the SQL files Goose applies, ordered by their number
// prefix, e.g. 00001_create_probe.sql. They are built into the binary.
//
//go:embed migrations/*.sql
var migrations embed.FS

const (
	// A waiting Migrate checks the lock every second for up to 5 minutes:
	// longer than the workflow tables may take, plus inbok's files.
	lockRetrySeconds = 1
	lockRetries      = 300

	// workflowTablesTimeout bounds the workflow tables' migration, as DBOS
	// does by default when it migrates at startup. DBOS retries a lost
	// connection without limit, which would leave a release hanging.
	workflowTablesTimeout = 2 * time.Minute
)

// Migrate applies every pending migration to the database at url: inbok's
// own files, then the workflow tables DBOS keeps in the "dbos" schema. It
// logs what it applied to logger and returns the first error, joined with
// any error releasing the lock.
//
// NOTE: DBOS takes no lock while migrating its tables, and two runs at once
// fail on each other's CREATE. One PostgreSQL lock covers both steps, so a
// second Migrate on the same database waits, then finds nothing to do.
func Migrate(ctx context.Context, url string, logger *slog.Logger) (err error) {
	// Goose needs database/sql; the same settings as Open give it the same
	// connect timeout.
	cfg, err := poolConfig(url)
	if err != nil {
		return err
	}

	db := stdlib.OpenDB(*cfg.ConnConfig)
	defer db.Close()

	unlock, err := lockMigrations(ctx, db)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()

	if err := migrateFiles(ctx, db, logger); err != nil {
		return err
	}
	return migrateWorkflowTables(ctx, url, logger)
}

// lockMigrations takes Goose's PostgreSQL lock on a connection of db,
// waiting for another Migrate to finish. It returns the function that
// releases the lock and the connection.
func lockMigrations(ctx context.Context, db *sql.DB) (func() error, error) {
	// Goose's default 5-second retry would delay a waiting Migrate
	// needlessly.
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(lockRetrySeconds, lockRetries))
	if err != nil {
		return nil, err
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if err := locker.SessionLock(ctx, conn); err != nil {
		return nil, errors.Join(err, conn.Close())
	}

	return func() error {
		// Not ctx: a cancelled Migrate still releases the lock.
		err := locker.SessionUnlock(context.WithoutCancel(ctx), conn)
		return errors.Join(err, conn.Close())
	}, nil
}

// migrateFiles applies inbok's pending migration files on db and logs each
// to logger.
func migrateFiles(ctx context.Context, db *sql.DB, logger *slog.Logger) error {
	provider, err := newProvider(db)
	if err != nil {
		return err
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return err
	}

	if len(results) == 0 {
		logger.LogAttrs(ctx, slog.LevelInfo, "database up to date")
	}
	for _, r := range results {
		logger.LogAttrs(ctx, slog.LevelInfo, "applied migration",
			slog.String("file", r.Source.Path), slog.Duration("duration", r.Duration))
	}

	return nil
}

// migrateWorkflowTables creates or upgrades DBOS's workflow tables at url,
// in its default schema "dbos", to the version the linked SDK needs. DBOS
// logs to logger.
//
// NOTE: DBOS versions its own tables and ships their migrations inside the
// SDK, so they are not Goose files. Each SDK upgrade can bring new ones;
// Migrate applies them in the release migration like inbok's own.
func migrateWorkflowTables(ctx context.Context, url string, logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, workflowTablesTimeout)
	defer cancel()

	// DBOS logs the URL, hiding only a password written before the host.
	dbosLogger := logging.WithoutAttrs(logger, "database_url")
	if err := dbos.Migrate(ctx, url, dbos.WithMigrateLogger(dbosLogger)); err != nil {
		return fmt.Errorf("migrate workflow tables: %w", err)
	}

	// Logged after every run: DBOS does not report what it applied.
	logger.LogAttrs(ctx, slog.LevelInfo, "workflow tables up to date")
	return nil
}

// newProvider reads the embedded migrations. It takes no lock: Migrate
// holds one around it.
func newProvider(db *sql.DB) (*goose.Provider, error) {
	dir, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}

	return goose.NewProvider(goose.DialectPostgres, db, dir)
}
