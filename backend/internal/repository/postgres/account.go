package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"inbok/internal/account"
	"inbok/internal/repository/postgres/sqlc"
)

// ReadCredentials loads an account and its hash using case-insensitive email.
// Missing accounts return account.ErrUserNotFound.
func (db *DB) ReadCredentials(ctx context.Context, email string) (account.Credentials, error) {
	row, err := sqlc.New(db.pool).ReadUserCredentials(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return account.Credentials{}, account.ErrUserNotFound
	}
	if err != nil {
		return account.Credentials{}, fmt.Errorf("read user credentials: %w", err)
	}
	return account.Credentials{
		User: account.User{ID: row.ID, Email: row.Email}, PasswordHash: row.PasswordHash,
	}, nil
}
