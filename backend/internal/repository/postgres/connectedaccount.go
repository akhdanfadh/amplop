package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"inbok/internal/connectedaccount"
	"inbok/internal/repository/postgres/sqlc"
)

// Token access reads and writes credentials here; this fails to compile if
// DB stops providing what connectedaccount.NewAccess takes.
var _ = connectedaccount.NewAccess((*DB)(nil), nil, nil, nil)

// InsertConnectedAccount saves account with the credential seal returns for
// its ID, and returns the saved account. It returns
// connectedaccount.ErrAlreadyConnected when account.UserID already has
// account.Subject from account.Provider; the ID taken is then never used.
func (db *DB) InsertConnectedAccount(ctx context.Context, account connectedaccount.NewAccount, seal func(int64) (connectedaccount.Credential, error)) (connectedaccount.Account, error) {
	queries := sqlc.New(db.pool)

	// 1. Take the ID first: the sealed token is bound to it.
	id, err := queries.NextConnectedAccountID(ctx)
	if err != nil {
		return connectedaccount.Account{}, fmt.Errorf("take connected account id: %w", err)
	}

	// 2. Seal the token for that ID.
	credential, err := seal(id)
	if err != nil {
		return connectedaccount.Account{}, err
	}

	// 3. Insert, unless the user already has the provider's subject.
	row, err := queries.InsertConnectedAccount(ctx, sqlc.InsertConnectedAccountParams{
		ID:                     id,
		UserID:                 account.UserID,
		Provider:               string(account.Provider),
		Email:                  account.Email,
		OauthSubject:           account.Subject,
		RefreshTokenKeyID:      credential.KeyID,
		RefreshTokenCiphertext: credential.Ciphertext,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return connectedaccount.Account{}, connectedaccount.ErrAlreadyConnected
	}
	if err != nil {
		return connectedaccount.Account{}, fmt.Errorf("insert connected account: %w", err)
	}
	return connectedAccountFromRow(sqlc.ListConnectedAccountsRow(row)), nil
}

// UpdateConnectedAccount saves, for userID's account accountID, the email and
// credential update returns, clears its need to reconnect, and returns it,
// in one transaction. It returns connectedaccount.ErrNotFound when userID has
// no such account.
func (db *DB) UpdateConnectedAccount(ctx context.Context, userID, accountID int64, update func(connectedaccount.Account) (connectedaccount.Reconnection, error)) (connectedaccount.Account, error) {
	var saved connectedaccount.Account
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the account, so two reconnects of it save one after the
		// other.
		row, err := queries.LockConnectedAccount(ctx, sqlc.LockConnectedAccountParams{ID: accountID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return connectedaccount.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock connected account: %w", err)
		}

		// 2. Let update decide.
		next, err := update(connectedAccountFromRow(sqlc.ListConnectedAccountsRow(row)))
		if err != nil {
			return err
		}

		// 3. Save the email and token update returns.
		updated, err := queries.UpdateConnectedAccount(ctx, sqlc.UpdateConnectedAccountParams{
			ID:                     accountID,
			UserID:                 userID,
			Email:                  next.Email,
			RefreshTokenKeyID:      next.Credential.KeyID,
			RefreshTokenCiphertext: next.Credential.Ciphertext,
		})
		if err != nil {
			return fmt.Errorf("update connected account: %w", err)
		}
		saved = connectedAccountFromRow(sqlc.ListConnectedAccountsRow(updated))
		return nil
	})
	if err != nil {
		return connectedaccount.Account{}, err
	}
	return saved, nil
}

// ListConnectedAccounts returns userID's accounts, oldest first.
func (db *DB) ListConnectedAccounts(ctx context.Context, userID int64) ([]connectedaccount.Account, error) {
	rows, err := sqlc.New(db.pool).ListConnectedAccounts(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list connected accounts: %w", err)
	}
	accounts := make([]connectedaccount.Account, 0, len(rows))
	for _, row := range rows {
		accounts = append(accounts, connectedAccountFromRow(row))
	}
	return accounts, nil
}

// DeleteConnectedAccount deletes userID's account accountID.
func (db *DB) DeleteConnectedAccount(ctx context.Context, userID, accountID int64) error {
	deleted, err := sqlc.New(db.pool).DeleteConnectedAccount(ctx, sqlc.DeleteConnectedAccountParams{ID: accountID, UserID: userID})
	if err != nil {
		return fmt.Errorf("delete connected account: %w", err)
	}
	if deleted == 0 {
		return connectedaccount.ErrNotFound
	}
	return nil
}

// ReadConnectedAccountAccess returns account accountID's credential and whether its
// provider work has stopped. It returns connectedaccount.ErrNotFound when
// there is no such account.
func (db *DB) ReadConnectedAccountAccess(ctx context.Context, accountID int64) (connectedaccount.AccessState, error) {
	row, err := sqlc.New(db.pool).ReadConnectedAccountAccess(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return connectedaccount.AccessState{}, connectedaccount.ErrNotFound
	}
	if err != nil {
		return connectedaccount.AccessState{}, fmt.Errorf("read access state: %w", err)
	}
	return connectedaccount.AccessState{
		UserID:         row.UserID,
		Credential:     connectedaccount.Credential{KeyID: row.RefreshTokenKeyID, Ciphertext: row.RefreshTokenCiphertext},
		NeedsReconnect: row.NeedsReconnect,
		Deactivated:    row.Deactivated,
	}, nil
}

// ReplaceConnectedAccountToken saves next as account accountID's credential if its
// ciphertext is still current, and returns
// connectedaccount.ErrCredentialChanged otherwise.
func (db *DB) ReplaceConnectedAccountToken(ctx context.Context, accountID int64, current []byte, next connectedaccount.Credential) error {
	replaced, err := sqlc.New(db.pool).ReplaceConnectedAccountToken(ctx, sqlc.ReplaceConnectedAccountTokenParams{
		ID:                     accountID,
		CurrentCiphertext:      current,
		RefreshTokenKeyID:      next.KeyID,
		RefreshTokenCiphertext: next.Ciphertext,
	})
	if err != nil {
		return fmt.Errorf("replace refresh token: %w", err)
	}
	if replaced == 0 {
		return connectedaccount.ErrCredentialChanged
	}
	return nil
}

// MarkConnectedAccountReconnect records at as when account accountID started needing a
// reconnect, keeping an earlier time, if its ciphertext is still current,
// and returns connectedaccount.ErrCredentialChanged otherwise.
func (db *DB) MarkConnectedAccountReconnect(ctx context.Context, accountID int64, current []byte, at time.Time) error {
	marked, err := sqlc.New(db.pool).MarkConnectedAccountReconnect(ctx, sqlc.MarkConnectedAccountReconnectParams{
		ID:                accountID,
		CurrentCiphertext: current,
		At:                at,
	})
	if err != nil {
		return fmt.Errorf("mark connected account needs reconnect: %w", err)
	}
	if marked == 0 {
		return connectedaccount.ErrCredentialChanged
	}
	return nil
}

// connectedAccountFromRow converts a row to an account. Every query returns the
// same columns, so each row type converts to this one.
func connectedAccountFromRow(row sqlc.ListConnectedAccountsRow) connectedaccount.Account {
	account := connectedaccount.Account{
		ID:        row.ID,
		UserID:    row.UserID,
		Provider:  connectedaccount.Provider(row.Provider),
		Email:     row.Email,
		Subject:   row.OauthSubject,
		CreatedAt: row.CreatedAt.UTC(),
	}
	if row.NeedsReconnectSince != nil {
		since := row.NeedsReconnectSince.UTC()
		account.NeedsReconnectSince = &since
	}
	return account
}
