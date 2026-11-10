package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"inbok/internal/outgoingmessage"
	"inbok/internal/repository/postgres/sqlc"
)

// storedRecipient is one address of an outgoing message's list as the
// table keeps it, the shape of JMAP's EmailAddress. Name is empty until the
// compose form takes names.
type storedRecipient struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// InsertOutgoingMessage locks the member attempt.UserID, reads the member's
// account accountID under a key share lock and the member's attempts, calls
// build with them, and saves the message build returns as waiting, with its
// job and the attempt, in one transaction. It returns the saved message,
// [outgoingmessage.ErrDeactivated], or [outgoingmessage.ErrAccountNotFound]
// when the member has no such account.
func (db *DB) InsertOutgoingMessage(ctx context.Context, attempt outgoingmessage.Attempt, accountID int64, build func(outgoingmessage.CreateState) (outgoingmessage.NewMessage, error)) (outgoingmessage.Message, error) {
	var saved outgoingmessage.Message
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the member before the account, as connecting does, so
		// the member's attempts are counted one at a time.
		if err := lockSendingMember(ctx, queries, attempt.UserID); err != nil {
			return err
		}

		// 2. Lock the account, so a disconnect waits and then deletes the
		// message with it.
		account, err := queries.LockOutgoingMessageSender(ctx, sqlc.LockOutgoingMessageSenderParams{ID: accountID, UserID: attempt.UserID})
		if errors.Is(err, pgx.ErrNoRows) {
			return outgoingmessage.ErrAccountNotFound
		}
		if err != nil {
			return fmt.Errorf("lock sending account: %w", err)
		}
		attempts, err := queries.ListSendAttemptTimes(ctx, sqlc.ListSendAttemptTimesParams{UserID: attempt.UserID, Since: attempt.Since})
		if err != nil {
			return fmt.Errorf("list send attempts: %w", err)
		}

		// 3. Let build decide.
		sender := outgoingmessage.Sender{ConnectedAccountID: account.ID, Email: account.Email, NeedsReconnect: account.NeedsReconnect}
		next, err := build(outgoingmessage.CreateState{Sender: sender, Attempts: attempts})
		if err != nil {
			return err
		}

		// 4. Save the message, its job, and the attempt.
		saved, err = db.saveOutgoingMessage(ctx, tx, attempt, accountID, next)
		return err
	})
	if err != nil {
		return outgoingmessage.Message{}, err
	}
	return saved, nil
}

// lockSendingMember locks userID's row, or returns
// [outgoingmessage.ErrDeactivated] when the member is deactivated or gone.
func lockSendingMember(ctx context.Context, queries *sqlc.Queries, userID int64) error {
	deactivated, err := queries.LockSendingMember(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && deactivated {
		return outgoingmessage.ErrDeactivated
	}
	if err != nil {
		return fmt.Errorf("lock sending member: %w", err)
	}
	return nil
}

// saveOutgoingMessage saves next in tx as waiting, from account accountID,
// then its job and attempt, deleting the member's attempts no window counts.
// It returns the saved message.
func (db *DB) saveOutgoingMessage(ctx context.Context, tx pgx.Tx, attempt outgoingmessage.Attempt, accountID int64, next outgoingmessage.NewMessage) (outgoingmessage.Message, error) {
	queries := sqlc.New(tx)

	// 1. Save the message.
	to, err := recipientsJSON(next.To)
	if err != nil {
		return outgoingmessage.Message{}, err
	}
	cc, err := recipientsJSON(next.Cc)
	if err != nil {
		return outgoingmessage.Message{}, err
	}
	bcc, err := recipientsJSON(next.Bcc)
	if err != nil {
		return outgoingmessage.Message{}, err
	}
	row, err := queries.InsertOutgoingMessage(ctx, sqlc.InsertOutgoingMessageParams{
		ConnectedAccountID: accountID,
		FromAddress:        next.From,
		ToAddresses:        to,
		CcAddresses:        cc,
		BccAddresses:       bcc,
		Subject:            next.Subject,
		Body:               next.Body,
		InbokMessageID:     next.InbokMessageID,
	})
	if err != nil {
		return outgoingmessage.Message{}, fmt.Errorf("insert outgoing message: %w", err)
	}

	// 2. Count the attempt, and forget those no window counts.
	if err := queries.InsertSendAttempt(ctx, sqlc.InsertSendAttemptParams{UserID: attempt.UserID, CreatedAt: attempt.At}); err != nil {
		return outgoingmessage.Message{}, fmt.Errorf("insert send attempt: %w", err)
	}
	if err := queries.DeleteOldSendAttempts(ctx, sqlc.DeleteOldSendAttemptsParams{UserID: attempt.UserID, Since: attempt.Since}); err != nil {
		return outgoingmessage.Message{}, fmt.Errorf("delete old send attempts: %w", err)
	}

	// 3. Save the job in this transaction, so it commits or rolls back with
	// the message.
	if db.workflows == nil {
		return outgoingmessage.Message{}, errNoWorkflows
	}
	if err := db.workflows.EnqueueOutgoingMessage(ctx, tx, row.ID); err != nil {
		return outgoingmessage.Message{}, fmt.Errorf("save outgoing message send: %w", err)
	}
	return outgoingmessage.Message{
		ID:                 row.ID,
		ConnectedAccountID: accountID,
		From:               next.From,
		Content:            next.Content,
		InbokMessageID:     next.InbokMessageID,
		Status:             outgoingmessage.Status(row.Status),
		CreatedAt:          row.CreatedAt,
	}, nil
}

// ReadOutgoingMessage returns userID's message messageID, or
// [outgoingmessage.ErrNotFound] when userID has no such message.
func (db *DB) ReadOutgoingMessage(ctx context.Context, userID int64, messageID uuid.UUID) (outgoingmessage.Message, error) {
	row, err := sqlc.New(db.pool).ReadOutgoingMessage(ctx, sqlc.ReadOutgoingMessageParams{ID: messageID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return outgoingmessage.Message{}, outgoingmessage.ErrNotFound
	}
	if err != nil {
		return outgoingmessage.Message{}, fmt.Errorf("read outgoing message: %w", err)
	}
	return outgoingMessageFromRow(row)
}

// ReadOutgoingMessageToSend returns message messageID, whoever owns it, or
// [outgoingmessage.ErrNotFound] when there is none.
func (db *DB) ReadOutgoingMessageToSend(ctx context.Context, messageID uuid.UUID) (outgoingmessage.Message, error) {
	row, err := sqlc.New(db.pool).ReadOutgoingMessageToSend(ctx, messageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return outgoingmessage.Message{}, outgoingmessage.ErrNotFound
	}
	if err != nil {
		return outgoingmessage.Message{}, fmt.Errorf("read outgoing message: %w", err)
	}
	return outgoingMessageFromRow(row)
}

// UpdateOutgoingMessage reads message messageID's sending member under the
// member's share lock, then locks the message, calls update with both, and
// saves the transition update returns, with the sender's address on the
// account when it names one, in one transaction. It returns the saved
// message, or [outgoingmessage.ErrNotFound] when there is no such message.
func (db *DB) UpdateOutgoingMessage(ctx context.Context, messageID uuid.UUID, update func(outgoingmessage.SendState) (outgoingmessage.Transition, error)) (outgoingmessage.Message, error) {
	var saved outgoingmessage.Message
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Read whether the member is deactivated, holding off a
		// deactivation until this transaction ends. The member is locked
		// before the message, as connecting locks them before the account
		// and a disconnect the account before the message: the other order
		// lets a send, a connect, and a disconnect wait on each other.
		deactivated, err := queries.LockOutgoingMessageMember(ctx, messageID)
		if errors.Is(err, pgx.ErrNoRows) {
			return outgoingmessage.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock sending member: %w", err)
		}

		// 2. Lock the message, so the steps of one send save one after
		// the other, and a disconnect waits and then deletes it. A
		// disconnect since step 1 has already deleted it.
		row, err := queries.LockOutgoingMessage(ctx, messageID)
		if errors.Is(err, pgx.ErrNoRows) {
			return outgoingmessage.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock outgoing message: %w", err)
		}
		current, err := outgoingMessageFromRow(row)
		if err != nil {
			return err
		}

		// 3. Let update decide.
		next, err := update(outgoingmessage.SendState{Message: current, SenderDeactivated: deactivated})
		if err != nil {
			return err
		}

		// 4. Save the message's state, and the account's address.
		updated, err := queries.UpdateOutgoingMessageStatus(ctx, sqlc.UpdateOutgoingMessageStatusParams{
			ID:                messageID,
			Status:            string(next.Status),
			Failure:           nullable(string(next.Failure)),
			ProviderMessageID: nullable(next.Sent.ProviderMessageID),
			ProviderThreadID:  nullable(next.Sent.ProviderThreadID),
			SentAt:            next.SentAt,
		})
		if err != nil {
			return fmt.Errorf("update outgoing message: %w", err)
		}
		if next.SenderEmail != "" {
			err := queries.UpdateConnectedAccountEmail(ctx, sqlc.UpdateConnectedAccountEmailParams{ID: row.ConnectedAccountID, Email: next.SenderEmail})
			if err != nil {
				return fmt.Errorf("update sending account's address: %w", err)
			}
		}
		saved, err = outgoingMessageFromRow(updated)
		return err
	})
	if err != nil {
		return outgoingmessage.Message{}, err
	}
	return saved, nil
}

// nullable returns value, or nil when it is empty.
func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// outgoingMessageFromRow returns row as a message, or an error when a
// recipient list is not the JSON the table keeps.
func outgoingMessageFromRow(row sqlc.OutgoingMessage) (outgoingmessage.Message, error) {
	to, err := recipientsFromJSON(row.ToAddresses)
	if err != nil {
		return outgoingmessage.Message{}, err
	}
	cc, err := recipientsFromJSON(row.CcAddresses)
	if err != nil {
		return outgoingmessage.Message{}, err
	}
	bcc, err := recipientsFromJSON(row.BccAddresses)
	if err != nil {
		return outgoingmessage.Message{}, err
	}
	message := outgoingmessage.Message{
		ID:                 row.ID,
		ConnectedAccountID: row.ConnectedAccountID,
		From:               row.FromAddress,
		Content: outgoingmessage.Content{
			To:      to,
			Cc:      cc,
			Bcc:     bcc,
			Subject: row.Subject,
			Body:    row.Body,
		},
		InbokMessageID: row.InbokMessageID,
		Status:         outgoingmessage.Status(row.Status),
		CreatedAt:      row.CreatedAt,
		SentAt:         row.SentAt,
	}
	if row.Failure != nil {
		message.Failure = outgoingmessage.Failure(*row.Failure)
	}
	if row.ProviderMessageID != nil {
		message.ProviderMessageID = *row.ProviderMessageID
	}
	if row.ProviderThreadID != nil {
		message.ProviderThreadID = *row.ProviderThreadID
	}
	return message, nil
}

// recipientsJSON returns addresses as the table's JSON array, each with an
// empty name.
func recipientsJSON(addresses []string) ([]byte, error) {
	stored := make([]storedRecipient, 0, len(addresses))
	for _, address := range addresses {
		stored = append(stored, storedRecipient{Address: address})
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("encode recipients: %w", err)
	}
	return raw, nil
}

// recipientsFromJSON returns the addresses in raw, the table's JSON array,
// never nil.
func recipientsFromJSON(raw []byte) ([]string, error) {
	var stored []storedRecipient
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("decode recipients: %w", err)
	}
	addresses := make([]string, 0, len(stored))
	for _, recipient := range stored {
		addresses = append(addresses, recipient.Address)
	}
	return addresses, nil
}
