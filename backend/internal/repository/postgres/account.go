package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"inbok/internal/account"
	"inbok/internal/repository/postgres/sqlc"
)

// ReadCredentials loads an account and its hash by its exact email, as
// emailaddress.Parse returns it.
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
		User: account.User{ID: row.ID, Email: row.Email, Role: account.Role(row.Role)}, PasswordHash: row.PasswordHash,
	}, nil
}

// ReadUser loads current account data by ID without reading its password hash.
// Missing accounts return account.ErrUserNotFound.
func (db *DB) ReadUser(ctx context.Context, id int64) (account.User, error) {
	row, err := sqlc.New(db.pool).ReadUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return account.User{}, account.ErrUserNotFound
	}
	if err != nil {
		return account.User{}, fmt.Errorf("read user: %w", err)
	}
	return account.User{ID: row.ID, Email: row.Email, Role: account.Role(row.Role)}, nil
}
