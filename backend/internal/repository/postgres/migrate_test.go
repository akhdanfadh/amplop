package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// The first run applies the files and logs each; the second has nothing to do.
func TestMigrate(t *testing.T) {
	url := newDatabase(t)

	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, nil))
	for range 2 {
		require.NoError(t, Migrate(t.Context(), url, logger))
	}

	logs := out.String()
	assert.Contains(t, logs, `msg="applied migration" file=00001_create_probe.sql`)
	assert.Contains(t, logs, `msg="database up to date"`)

	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())

	var total int64
	err = conn.QueryRow(t.Context(), "SELECT total FROM probe_counter WHERE id = 1").Scan(&total)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}

// Two migrations at once, e.g. two deploys, must not apply a file twice.
// Goose's version table must exist first: creating it, Goose retries
// after a clash, which alone spaces out the runs.
func TestMigrateConcurrent(t *testing.T) {
	// Given Goose's version table and two migrations waiting on one start
	// signal.
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	_, err := provider.DownTo(t.Context(), 0)
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
}

// Every Down section works: undoing all migrations removes the probe
// tables, and the files apply again afterwards.
func TestMigrateDown(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	_, err := provider.DownTo(t.Context(), 0)
	require.NoError(t, err)

	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())

	var tables int
	err = conn.QueryRow(t.Context(), "SELECT count(*) FROM pg_tables WHERE tablename IN ('probe_counter', 'probe_events')").Scan(&tables)
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

// The database rejects rows the API would reject.
func TestProbeChecks(t *testing.T) {
	conn := migrated(t)

	const insert = "INSERT INTO probe_events (note, amount, highlighted, tag) VALUES ($1, $2, false, $3)"
	valid := func(tag any) error {
		_, err := conn.Exec(t.Context(), insert, "hello", 1, tag)
		return err
	}
	require.NoError(t, valid(nil), "no tag")
	require.NoError(t, valid(strings.Repeat("t", 40)), "40-character tag")

	cases := []struct {
		name, sql string
		args      []any
	}{
		{"blank note", insert, []any{" \t\n", 1, nil}},
		{"long note", insert, []any{strings.Repeat("n", 121), 1, nil}},
		{"zero amount", insert, []any{"hello", 0, nil}},
		{"large amount", insert, []any{"hello", 11, nil}},
		{"empty tag", insert, []any{"hello", 1, ""}},
		{"long tag", insert, []any{"hello", 1, strings.Repeat("t", 41)}},
		{"second counter", "INSERT INTO probe_counter (id, total) VALUES (2, 0)", nil},
		{"negative total", "UPDATE probe_counter SET total = -1", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := conn.Exec(t.Context(), c.sql, c.args...)

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Equal(t, checkViolation, pgErr.Code)
		})
	}
}
