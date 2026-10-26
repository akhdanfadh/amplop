package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/probe"
)

// probeService returns the probe service on a fresh, migrated database,
// plus a separate connection for checking the saved rows.
func probeService(t *testing.T) (*probe.Service, *pgx.Conn) {
	t.Helper()

	conn := migrated(t)
	db, err := Open(t.Context(), conn.Config().ConnString())
	require.NoError(t, err)
	t.Cleanup(db.Close)

	return probe.NewService(db), conn
}

// saved returns the counter's total, the number of events, and the sum of
// their amounts.
func saved(t *testing.T, conn *pgx.Conn) (total, events, amounts int64) {
	t.Helper()

	err := conn.QueryRow(t.Context(), `
		SELECT (SELECT total FROM probe_counter),
		       count(*),
		       coalesce(sum(amount), 0)
		FROM probe_events`).Scan(&total, &events, &amounts)
	require.NoError(t, err)
	return total, events, amounts
}

func TestUpdateCounter(t *testing.T) {
	service, conn := probeService(t)
	tag := "demo"

	first, err := service.Add(t.Context(), probe.Entry{Note: "hello", Amount: 3, Highlighted: true, Tag: &tag})
	require.NoError(t, err)
	second, err := service.Add(t.Context(), probe.Entry{Note: "again", Amount: 4})
	require.NoError(t, err)

	// Equal compares what pointers point to, not their addresses.
	assert.Equal(t, int64(3), first.Total)
	assert.NotZero(t, first.Event.ID)
	assert.NotZero(t, first.Event.CreatedAt)
	assert.Equal(t, &tag, first.Event.Tag)
	assert.Equal(t, int64(7), second.Total)
	assert.NotEqual(t, first.Event.ID, second.Event.ID)
	assert.Nil(t, second.Event.Tag)

	// A NULL tag round-trips as nil.
	rows, err := conn.Query(t.Context(), "SELECT tag FROM probe_events ORDER BY id")
	require.NoError(t, err)
	tags, err := pgx.CollectRows(rows, pgx.RowTo[*string])
	require.NoError(t, err)
	assert.Equal(t, []*string{&tag, nil}, tags)

	total, events, _ := saved(t, conn)
	assert.Equal(t, int64(7), total)
	assert.Equal(t, int64(2), events)
}

// A rule the update rejects saves nothing.
func TestUpdateCounterFull(t *testing.T) {
	service, conn := probeService(t)
	_, err := conn.Exec(t.Context(), "UPDATE probe_counter SET total = 995")
	require.NoError(t, err)

	_, err = service.Add(t.Context(), probe.Entry{Note: "too much", Amount: 6})
	require.ErrorIs(t, err, probe.ErrCounterFull)

	total, events, _ := saved(t, conn)
	assert.Equal(t, int64(995), total)
	assert.Equal(t, int64(0), events)
}

// If the last write fails, the event written before it is rolled back too.
func TestUpdateCounterRollback(t *testing.T) {
	// Given a test-only trigger that makes saving the total fail after the
	// insert.
	service, conn := probeService(t)
	_, err := conn.Exec(t.Context(), `
		CREATE FUNCTION fail() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'forced failure'; END $$;
		CREATE TRIGGER fail BEFORE UPDATE ON probe_counter
		FOR EACH ROW EXECUTE FUNCTION fail();`)
	require.NoError(t, err)

	// When an entry is added.
	_, err = service.Add(t.Context(), probe.Entry{Note: "lost", Amount: 1})

	// Then the add fails and neither the total nor the event is saved.
	require.ErrorContains(t, err, "forced failure")
	total, events, _ := saved(t, conn)
	assert.Equal(t, int64(0), total)
	assert.Equal(t, int64(0), events)
}

// Concurrent adds never lose an increment: the total equals the sum of
// the saved amounts, and every add saved one event.
func TestUpdateCounterConcurrent(t *testing.T) {
	// Given 20 adds waiting on one start signal.
	service, conn := probeService(t)

	const adds = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, adds)
	var want int64
	for i := range adds {
		amount := i%10 + 1
		want += int64(amount)
		wg.Go(func() {
			<-start
			_, errs[i] = service.Add(context.Background(), probe.Entry{Note: "race", Amount: amount})
		})
	}

	// When they start together. Each saves its own error: require must not
	// run outside the test's goroutine.
	close(start)
	wg.Wait()

	// Then every add succeeds and is counted once.
	require.NoError(t, errors.Join(errs...))
	total, events, amounts := saved(t, conn)
	assert.Equal(t, want, total)
	assert.Equal(t, int64(adds), events)
	assert.Equal(t, want, amounts)
}

// Open fails at once when nothing listens, instead of on first use.
func TestOpenUnreachable(t *testing.T) {
	db, err := Open(t.Context(), "postgres://inbok:secret@127.0.0.1:1/inbok?sslmode=disable")
	if err == nil {
		db.Close()
	}

	require.Error(t, err)
}
