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

// InsertConnectedAccount saves account with the credential seal returns for
// its ID, with its sync state, waiting, and the job of its first full sync,
// in one transaction, and returns the saved account. It returns
// [connectedaccount.ErrAlreadyConnected] when account.UserID already has
// account.Subject from account.Provider; the ID taken is then never used.
func (db *DB) InsertConnectedAccount(ctx context.Context, account connectedaccount.NewAccount, seal func(int64) (connectedaccount.Credential, error)) (connectedaccount.Account, error) {
	var saved connectedaccount.Account
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Take the ID first: the sealed token is bound to it.
		id, err := queries.NextConnectedAccountID(ctx)
		if err != nil {
			return fmt.Errorf("take connected account id: %w", err)
		}

		// 2. Seal the token for that ID.
		credential, err := seal(id)
		if err != nil {
			return err
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
			return connectedaccount.ErrAlreadyConnected
		}
		if err != nil {
			return fmt.Errorf("insert connected account: %w", err)
		}

		// 4. Save the sync state and its first sync, a full one, in this
		// transaction, so no account is connected without them (ADR001).
		if err := queries.InsertSyncState(ctx, id); err != nil {
			return fmt.Errorf("insert sync state: %w", err)
		}
		if db.workflows == nil {
			return errNoWorkflows
		}
		if _, err := db.workflows.EnqueueSync(ctx, tx, id); err != nil {
			return fmt.Errorf("save sync: %w", err)
		}
		saved = connectedAccountFromRow(sqlc.InsertConnectedAccountRow(row))
		return nil
	})
	if err != nil {
		return connectedaccount.Account{}, err
	}
	return saved, nil
}

// UpdateConnectedAccount saves, for userID's account accountID, the email and
// credential update returns, clears its need to reconnect, and returns it,
// in one transaction. It returns [connectedaccount.ErrNotFound] when userID has
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
		next, err := update(connectedAccountFromRow(sqlc.InsertConnectedAccountRow(row)))
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
		saved = connectedAccountFromRow(sqlc.InsertConnectedAccountRow(updated))
		return nil
	})
	if err != nil {
		return connectedaccount.Account{}, err
	}
	return saved, nil
}

// ListConnectedAccounts returns userID's accounts, oldest first.
func (db *DB) ListConnectedAccounts(ctx context.Context, userID int64) ([]connectedaccount.StoredAccount, error) {
	rows, err := sqlc.New(db.pool).ListConnectedAccounts(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list connected accounts: %w", err)
	}
	accounts := make([]connectedaccount.StoredAccount, 0, len(rows))
	for _, row := range rows {
		accounts = append(accounts, storedAccountFromRow(row))
	}
	return accounts, nil
}

// DeleteConnectedAccount deletes userID's account accountID, moves its
// credential to an account removal, and saves the job that revokes it, in
// one transaction. It returns the deleted account, or
// [connectedaccount.ErrNotFound] when userID has no such account.
func (db *DB) DeleteConnectedAccount(ctx context.Context, userID, accountID int64) (connectedaccount.StoredAccount, error) {
	var deleted connectedaccount.StoredAccount
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Delete the account, which deletes its connect states with it.
		row, err := queries.DeleteConnectedAccount(ctx, sqlc.DeleteConnectedAccountParams{ID: accountID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return connectedaccount.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("delete connected account: %w", err)
		}

		// 2. Keep its credential for the job, which then needs only the
		// removal's ID (ADR003).
		removalID, err := queries.InsertAccountRemoval(ctx, sqlc.InsertAccountRemovalParams{
			UserID:                 row.UserID,
			AccountID:              row.ID,
			Provider:               row.Provider,
			OauthSubject:           row.OauthSubject,
			RefreshTokenKeyID:      row.RefreshTokenKeyID,
			RefreshTokenCiphertext: row.RefreshTokenCiphertext,
		})
		if err != nil {
			return fmt.Errorf("insert account removal: %w", err)
		}

		// 3. Save the job in this transaction, so it commits or rolls back
		// with the disconnect.
		if db.workflows == nil {
			return errNoWorkflows
		}
		if err := db.workflows.EnqueueAccountRemoval(ctx, tx, removalID); err != nil {
			return fmt.Errorf("save account removal: %w", err)
		}
		deleted = storedAccountFromRow(sqlc.ListConnectedAccountsRow(row))
		return nil
	})
	if err != nil {
		return connectedaccount.StoredAccount{}, err
	}
	return deleted, nil
}

// ReadAccountRemoval returns removal removalID, with whether a
// connected account is the same Google account, or
// [connectedaccount.ErrNotFound] when there is none.
func (db *DB) ReadAccountRemoval(ctx context.Context, removalID int64) (connectedaccount.Removal, error) {
	row, err := sqlc.New(db.pool).ReadAccountRemoval(ctx, removalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return connectedaccount.Removal{}, connectedaccount.ErrNotFound
	}
	if err != nil {
		return connectedaccount.Removal{}, fmt.Errorf("read account removal: %w", err)
	}
	return connectedaccount.Removal{
		UserID:     row.UserID,
		AccountID:  row.AccountID,
		Credential: connectedaccount.Credential{KeyID: row.RefreshTokenKeyID, Ciphertext: row.RefreshTokenCiphertext},
		Shared:     row.Shared,
	}, nil
}

// DeleteAccountRemoval deletes removal removalID; one already gone
// is no error.
func (db *DB) DeleteAccountRemoval(ctx context.Context, removalID int64) error {
	if err := sqlc.New(db.pool).DeleteAccountRemoval(ctx, removalID); err != nil {
		return fmt.Errorf("delete account removal: %w", err)
	}
	return nil
}

// ReadConnectedAccountAccess returns account accountID's credential and whether its
// provider work has stopped. It returns [connectedaccount.ErrNotFound] when
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
// [connectedaccount.ErrCredentialChanged] otherwise.
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
// and returns [connectedaccount.ErrCredentialChanged] otherwise.
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

// storedAccountFromRow converts a row with a credential to an account with
// it. List and delete return the same columns, so each row type converts to
// this one.
func storedAccountFromRow(row sqlc.ListConnectedAccountsRow) connectedaccount.StoredAccount {
	return connectedaccount.StoredAccount{
		Account: connectedAccountFromRow(sqlc.InsertConnectedAccountRow{
			ID: row.ID, UserID: row.UserID, Provider: row.Provider, Email: row.Email,
			OauthSubject: row.OauthSubject, NeedsReconnectSince: row.NeedsReconnectSince, CreatedAt: row.CreatedAt,
		}),
		Credential: connectedaccount.Credential{KeyID: row.RefreshTokenKeyID, Ciphertext: row.RefreshTokenCiphertext},
	}
}

// connectedAccountFromRow converts a row to an account. Every query without
// the credential returns the same columns, so each row type converts to this
// one.
func connectedAccountFromRow(row sqlc.InsertConnectedAccountRow) connectedaccount.Account {
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
