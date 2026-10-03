// Package postgres stores inbok's data in PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// migrations holds the SQL files Goose applies, ordered by their number
// prefix, e.g. 00001_create_probe.sql. They are built into the binary.
//
//go:embed migrations/*.sql
var migrations embed.FS

const (
	lockRetrySeconds = 1
	lockRetries      = 300
)

// Migrate applies every pending migration. A PostgreSQL lock makes a
// second Migrate on the same database wait, not apply the files twice.
func Migrate(ctx context.Context, url string, logger *slog.Logger) error {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer db.Close()

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

// newProvider reads the embedded migrations and locks the database while
// changing it.
func newProvider(db *sql.DB) (*goose.Provider, error) {
	// A waiting Migrate retries every second, for up to 5 minutes as by
	// default; Goose's default 5-second retry would delay it needlessly.
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(lockRetrySeconds, lockRetries))
	if err != nil {
		return nil, err
	}

	dir, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}

	return goose.NewProvider(goose.DialectPostgres, db, dir, goose.WithSessionLocker(locker))
}
