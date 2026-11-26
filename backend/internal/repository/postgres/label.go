package postgres

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"amplop/internal/connectedaccount"
	"amplop/internal/label"
	"amplop/internal/mailsync"
	"amplop/internal/repository/postgres/sqlc"
)

// ReadLabelAccount returns the account of userID's label id, or
// [label.ErrNotFound].
func (db *DB) ReadLabelAccount(ctx context.Context, userID int64, id uuid.UUID) (int64, error) {
	accountID, err := sqlc.New(db.pool).ReadMemberLabelAccount(ctx, sqlc.ReadMemberLabelAccountParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, label.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read label account: %w", err)
	}
	return accountID, nil
}

// ListLabels returns the labels of userID's account accountID by name, or
// [connectedaccount.ErrNotFound] when userID has no such account.
func (db *DB) ListLabels(ctx context.Context, userID, accountID int64) ([]label.Label, error) {
	var labels []label.Label
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		exists, err := queries.ReadMemberAccountExists(ctx, sqlc.ReadMemberAccountExistsParams{ConnectedAccountID: accountID, UserID: userID})
		if err != nil {
			return fmt.Errorf("read account: %w", err)
		}
		if !exists {
			return connectedaccount.ErrNotFound
		}
		rows, err := queries.ListAccountLabels(ctx, accountID)
		if err != nil {
			return fmt.Errorf("list labels: %w", err)
		}
		labels = make([]label.Label, 0, len(rows))
		for _, row := range rows {
			l := label.Label{ID: row.ID, AccountID: accountID, ProviderID: row.ProviderMailboxID, Name: row.Name}
			if row.ParentID != nil {
				l.Parent = *row.ParentID
			}
			labels = append(labels, l)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return labels, nil
}

// SaveMailboxes makes mailboxes accountID's mailboxes as
// [DB.UpdateSyncedMail] writes [mailsync.Change.Mailboxes], under the sync
// state's lock, keeping the state. It returns
// [connectedaccount.ErrNotFound] when the account is gone.
func (db *DB) SaveMailboxes(ctx context.Context, accountID int64, mailboxes []mailsync.Mailbox) error {
	// A nil list would leave the mailboxes as they are.
	if mailboxes == nil {
		mailboxes = []mailsync.Mailbox{}
	}
	_, err := db.UpdateSyncedMail(ctx, accountID, func(current mailsync.SyncState) (mailsync.Change, error) {
		return mailsync.Change{State: current, Mailboxes: mailboxes}, nil
	})
	if errors.Is(err, mailsync.ErrNotFound) {
		return connectedaccount.ErrNotFound
	}
	return err
}
