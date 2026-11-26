package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkViolation is PostgreSQL's error code for a failed CHECK.
const checkViolation = "23514"

var discard = slog.New(slog.DiscardHandler)

// migrated returns a new database with every migration applied.
func migrated(t *testing.T) *pgx.Conn {
	t.Helper()

	url := newDatabase(t)
	require.NoError(t, Migrate(t.Context(), url, discard))

	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

// The first run applies the files and logs each; the second has nothing to
// do. Afterwards DBOS opens the workflow tables without migrating them.
func TestMigrate(t *testing.T) {
	url := newDatabase(t)

	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, nil))
	for range 2 {
		require.NoError(t, Migrate(t.Context(), url, logger))
	}

	logs := out.String()
	assert.Contains(t, logs, `msg="applied migration" file=00002_create_users.sql`)
	assert.Contains(t, logs, `msg="database up to date"`)
	assert.Contains(t, logs, `msg="workflow tables up to date"`)
	requireWorkflowTables(t, url)

	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())

	var users int64
	err = conn.QueryRow(t.Context(), "SELECT count(*) FROM users").Scan(&users)
	require.NoError(t, err)
	assert.Equal(t, int64(0), users)
}

// A database migrated before inbok used DBOS, e.g. a running install
// upgrading, gets the workflow tables from its next [Migrate].
func TestMigrateUpgrade(t *testing.T) {
	// Given a database with inbok's tables but no workflow tables.
	url := newDatabase(t)
	db, err := sql.Open("pgx", url)
	require.NoError(t, err)
	defer db.Close()
	provider, err := newProvider(db)
	require.NoError(t, err)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	err = openWorkflowTables(t.Context(), url)
	require.ErrorContains(t, err, "is at schema version 0")

	// When it migrates.
	err = Migrate(t.Context(), url, discard)
	require.NoError(t, err)

	// Then DBOS opens it.
	requireWorkflowTables(t, url)
}

// Two migrations at once, e.g. two deploys, must not apply anything twice:
// the second waits for the first, then finds nothing to do. Goose's version
// table must exist first: creating it, Goose retries after a clash, which
// alone spaces out the runs.
func TestMigrateConcurrent(t *testing.T) {
	// Given Goose's version table, no workflow tables, and two migrations
	// waiting on one start signal.
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	_, err := provider.DownTo(t.Context(), 0)
	require.NoError(t, err)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())
	_, err = conn.Exec(t.Context(), "DROP SCHEMA dbos CASCADE")
	require.NoError(t, err)

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() {
			<-start
			errs[i] = Migrate(t.Context(), url, discard)
		})
	}

	// When they start together. Each saves its own error: require must not
	// run outside the test's goroutine.
	close(start)
	wg.Wait()

	// Then both succeed; a file applied twice would fail on its CREATE.
	require.NoError(t, errors.Join(errs...))
	requireWorkflowTables(t, url)
}

// requireWorkflowTables checks that DBOS opens the workflow tables at url
// without migrating them, as a process that is not the release migration
// does.
func requireWorkflowTables(t *testing.T, url string) {
	t.Helper()
	require.NoError(t, openWorkflowTables(t.Context(), url))
}

// openWorkflowTables opens DBOS on url without migrating, then closes it.
// It returns DBOS's error, e.g. for tables that are missing or too old.
func openWorkflowTables(ctx context.Context, url string) error {
	dbosCtx, err := dbos.NewContext(ctx, dbos.Config{
		AppName:        "inbok",
		DatabaseURL:    url,
		Logger:         discard,
		SkipMigrations: true,
	})
	if err != nil {
		return err
	}
	return dbos.Shutdown(dbosCtx, time.Second)
}

// Every Down section works: undoing all migrations removes the tables,
// and the files apply again afterwards.
func TestMigrateDown(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	_, err := provider.DownTo(t.Context(), 0)
	require.NoError(t, err)

	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())

	var tables int
	err = conn.QueryRow(t.Context(), "SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename <> 'goose_db_version'").Scan(&tables)
	require.NoError(t, err)
	assert.Equal(t, 0, tables)

	_, err = provider.Up(t.Context())
	require.NoError(t, err)
}

// migratedProvider applies every migration to url, then returns a Goose
// provider for it, e.g. to undo them.
func migratedProvider(t *testing.T, url string) *goose.Provider {
	t.Helper()

	require.NoError(t, Migrate(t.Context(), url, discard))

	db, err := sql.Open("pgx", url)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	provider, err := newProvider(db)
	require.NoError(t, err)
	return provider
}
