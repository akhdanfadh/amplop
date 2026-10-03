package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"inbok/internal/probe"
)

// probeService returns the probe service on a fresh, migrated database,
// plus a separate connection for checking the saved rows.
func probeService(t *testing.T) (*probe.Service, *pgx.Conn) {
	t.Helper()

	conn := migrated(t)
	db, err := Open(t.Context(), conn.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	return total, events, amounts
}

func TestUpdateCounter(t *testing.T) {
	service, conn := probeService(t)
	tag := "demo"

	first, err := service.Add(t.Context(), probe.Entry{Note: "hello", Amount: 3, Highlighted: true, Tag: &tag})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Add(t.Context(), probe.Entry{Note: "again", Amount: 4})
	if err != nil {
		t.Fatal(err)
	}

	if first.Total != 3 || first.Event.ID == 0 || first.Event.CreatedAt.IsZero() || *first.Event.Tag != "demo" {
		t.Fatalf("first = %+v", first)
	}
	if second.Total != 7 || second.Event.ID == first.Event.ID || second.Event.Tag != nil {
		t.Fatalf("second = %+v", second)
	}

	// A NULL tag round-trips as nil.
	rows, err := conn.Query(t.Context(), "SELECT tag FROM probe_events ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	tags, err := pgx.CollectRows(rows, pgx.RowTo[*string])
	if err != nil || len(tags) != 2 || *tags[0] != "demo" || tags[1] != nil {
		t.Fatalf("tags = %v, %v", tags, err)
	}

	if total, events, _ := saved(t, conn); total != 7 || events != 2 {
		t.Fatalf("saved total %d, %d events; want 7, 2", total, events)
	}
}

// A rule the update rejects saves nothing.
func TestUpdateCounterFull(t *testing.T) {
	service, conn := probeService(t)
	if _, err := conn.Exec(t.Context(), "UPDATE probe_counter SET total = 995"); err != nil {
		t.Fatal(err)
	}

	_, err := service.Add(t.Context(), probe.Entry{Note: "too much", Amount: 6})
	if !errors.Is(err, probe.ErrCounterFull) {
		t.Fatalf("err = %v, want %v", err, probe.ErrCounterFull)
	}

	if total, events, _ := saved(t, conn); total != 995 || events != 0 {
		t.Fatalf("saved total %d, %d events; want 995, 0", total, events)
	}
}

// If the last write fails, the event written before it is rolled back too.
func TestUpdateCounterRollback(t *testing.T) {
	service, conn := probeService(t)

	// A test-only trigger makes saving the total fail after the insert.
	_, err := conn.Exec(t.Context(), `
		CREATE FUNCTION fail() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'forced failure'; END $$;
		CREATE TRIGGER fail BEFORE UPDATE ON probe_counter
		FOR EACH ROW EXECUTE FUNCTION fail();`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Add(t.Context(), probe.Entry{Note: "lost", Amount: 1})
	if err == nil || !strings.Contains(err.Error(), "forced failure") {
		t.Fatalf("err = %v, want the forced failure", err)
	}

	if total, events, _ := saved(t, conn); total != 0 || events != 0 {
		t.Fatalf("saved total %d, %d events; want 0, 0", total, events)
	}
}

// Concurrent adds never lose an increment: the total equals the sum of
// the saved amounts, and every add saved one event.
func TestUpdateCounterConcurrent(t *testing.T) {
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
	close(start)
	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}

	total, events, amounts := saved(t, conn)
	if total != want || events != adds || amounts != want {
		t.Fatalf("total %d, %d events, amounts %d; want %d, %d, %d", total, events, amounts, want, adds, want)
	}
}

// Open fails at once when nothing listens, instead of on first use.
func TestOpenUnreachable(t *testing.T) {
	db, err := Open(t.Context(), "postgres://inbok:secret@127.0.0.1:1/inbok?sslmode=disable")
	if err == nil {
		db.Close()
		t.Fatal("Open succeeded without a database")
	}
}
