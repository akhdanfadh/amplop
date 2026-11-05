package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"inbok/internal/account"
	"inbok/internal/repository/postgres/sqlc"
)

// ReadSetup reports whether an administrator has been saved.
func (db *DB) ReadSetup(ctx context.Context) (account.SetupState, error) {
	return readSetup(ctx, sqlc.New(db.pool))
}

// readSetup uses either the pool for status or a transaction after locking.
func readSetup(ctx context.Context, queries *sqlc.Queries) (account.SetupState, error) {
	exists, err := queries.HasAdminUser(ctx)
	if err != nil {
		return account.SetupState{}, fmt.Errorf("read setup state: %w", err)
	}
	return account.SetupState{AdminExists: exists}, nil
}

// UpdateSetup inserts the administrator update returns, in one transaction,
// if update accepts the setup state it reads, and returns the new user.
func (db *DB) UpdateSetup(ctx context.Context, update func(account.SetupState) (account.NewAdmin, error)) (account.User, error) {
	var user account.User

	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock user writes, so claims run one at a time.
		if err := queries.LockUsers(ctx); err != nil {
			return fmt.Errorf("lock users: %w", err)
		}

		// 2. Read the setup state. Read Committed lets a claim that waited see
		// the winner's committed row.
		state, err := readSetup(ctx, queries)
		if err != nil {
			return err
		}

		// 3. Let update accept or refuse.
		admin, err := update(state)
		if err != nil {
			return err
		}

		// 4. Insert the administrator.
		row, err := queries.InsertAdminUser(ctx, sqlc.InsertAdminUserParams{
			Email: admin.Email, PasswordHash: admin.PasswordHash,
		})
		if err != nil {
			return fmt.Errorf("insert admin user: %w", err)
		}
		user = account.User{ID: row.ID, Email: row.Email, Role: account.Role(row.Role)}
		return nil
	})
	if err != nil {
		return account.User{}, err
	}

	return user, nil
}
