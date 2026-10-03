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
)

// checkViolation is PostgreSQL's error code for a failed CHECK.
const checkViolation = "23514"

var discard = slog.New(slog.DiscardHandler)

// migrated returns a new database with every migration applied.
func migrated(t *testing.T) *pgx.Conn {
	t.Helper()

	url := newDatabase(t)
	if err := Migrate(t.Context(), url, discard); err != nil {
		t.Fatal(err)
	}

	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

// The first run applies the files and logs each; the second has nothing to do.
func TestMigrate(t *testing.T) {
	url := newDatabase(t)

	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, nil))
	for range 2 {
		if err := Migrate(t.Context(), url, logger); err != nil {
			t.Fatal(err)
		}
	}

	logs := out.String()
	if !strings.Contains(logs, `msg="applied migration" file=00001_create_probe.sql`) ||
		!strings.Contains(logs, `msg="database up to date"`) {
		t.Fatalf("logs:\n%s", logs)
	}

	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(t.Context())

	var total int64
	if err := conn.QueryRow(t.Context(), "SELECT total FROM probe_counter WHERE id = 1").Scan(&total); err != nil || total != 0 {
		t.Fatalf("counter = %d, %v; want 0", total, err)
	}
}

// Two migrations at once, e.g. two deploys, must not apply a file twice.
// Goose's version table must exist first: creating it, Goose retries
// after a clash, which alone spaces out the runs.
func TestMigrateConcurrent(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	if _, err := provider.DownTo(t.Context(), 0); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() {
			<-start
			errs[i] = Migrate(t.Context(), url, discard)
		})
	}
	close(start)
	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
}

// Every Down section works: undoing all migrations removes the probe
// tables, and the files apply again afterwards.
func TestMigrateDown(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	if _, err := provider.DownTo(t.Context(), 0); err != nil {
		t.Fatal(err)
	}

	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(t.Context())

	var tables int
	err = conn.QueryRow(t.Context(), "SELECT count(*) FROM pg_tables WHERE tablename IN ('probe_counter', 'probe_events')").Scan(&tables)
	if err != nil || tables != 0 {
		t.Fatalf("probe tables left = %d, %v", tables, err)
	}

	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// migratedProvider applies every migration to url, then returns a Goose
// provider for it, e.g. to undo them.
func migratedProvider(t *testing.T, url string) *goose.Provider {
	t.Helper()

	if err := Migrate(t.Context(), url, discard); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	provider, err := newProvider(db)
	if err != nil {
		t.Fatal(err)
	}
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
	if err := valid(nil); err != nil {
		t.Fatalf("no tag: %v", err)
	}
	if err := valid(strings.Repeat("t", 40)); err != nil {
		t.Fatalf("40-character tag: %v", err)
	}

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
		_, err := conn.Exec(t.Context(), c.sql, c.args...)

		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != checkViolation {
			t.Errorf("%s: err = %v, want a CHECK violation", c.name, err)
		}
	}
}
