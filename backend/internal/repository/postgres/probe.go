package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"inbok/internal/probe"
	"inbok/internal/repository/postgres/sqlc"
)

// UpdateCounter implements [probe.Storage] in one transaction:
//
//	lock counter -> update(counter) -> insert event -> save total -> commit
//
// The lock makes a concurrent call wait at its own lock until this one
// commits, so it reads the new total. Any error rolls everything back.
func (db *DB) UpdateCounter(ctx context.Context, update func(c *probe.Counter) (probe.Entry, error)) (probe.Result, error) {
	var result probe.Result

	// BeginFunc commits when the function returns nil, else rolls back.
	err := pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		total, err := queries.LockProbeCounter(ctx)
		if err != nil {
			return fmt.Errorf("lock probe counter: %w", err)
		}

		counter := probe.Counter{Total: total}
		entry, err := update(&counter)
		if err != nil {
			return err
		}

		// The API allows an amount of 1 to 10, so int32 cannot overflow.
		row, err := queries.InsertProbeEvent(ctx, sqlc.InsertProbeEventParams{
			Note:        entry.Note,
			Amount:      int32(entry.Amount),
			Highlighted: entry.Highlighted,
			Tag:         entry.Tag,
		})
		if err != nil {
			return fmt.Errorf("insert probe event: %w", err)
		}

		if err := queries.UpdateProbeCounter(ctx, counter.Total); err != nil {
			return fmt.Errorf("save probe counter: %w", err)
		}

		result = probe.Result{
			Event: probe.Event{Entry: entry, ID: row.ID, CreatedAt: row.CreatedAt},
			Total: counter.Total,
		}
		return nil
	})
	if err != nil {
		return probe.Result{}, err
	}

	return result, nil
}
