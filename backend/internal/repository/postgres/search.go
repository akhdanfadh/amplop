package postgres

import (
	"context"
	"fmt"
	"uuid"

	"inbok/internal/mailsync"
	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/search"
)

// IndexSources returns member owner's messages among ids, with what their
// search documents need, in no set order. An ID owner has no message
// with is left out.
func (db *DB) IndexSources(ctx context.Context, owner int64, ids []uuid.UUID) ([]search.Source, error) {
	rows, err := sqlc.New(db.pool).ListIndexSources(ctx, sqlc.ListIndexSourcesParams{Owner: owner, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("list index sources: %w", err)
	}
	sources := make([]search.Source, 0, len(rows))
	for _, row := range rows {
		source := search.Source{
			ID:           row.ID,
			AccountID:    row.ConnectedAccountID,
			InternalDate: row.InternalDate.UTC(),
			Subject:      row.Subject,
			MailboxIDs:   row.MailboxIds,
			HasParts:     row.HasParts,
		}
		lists := []*[]mailsync.Address{&source.From, &source.To, &source.CC}
		for i, raw := range [][]byte{row.FromAddresses, row.ToAddresses, row.CcAddresses} {
			if *lists[i], err = decodeAddresses(raw); err != nil {
				return nil, err
			}
		}
		sources = append(sources, source)
	}
	return sources, nil
}
