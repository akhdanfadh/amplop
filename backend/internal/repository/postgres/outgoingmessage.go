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

// InsertOutgoingMessage reads userID's account accountID under a key share
// lock, calls build with it, and saves the message build returns as
// waiting, in one transaction. It returns the saved message, or
// [outgoingmessage.ErrAccountNotFound] when userID has no such account.
func (db *DB) InsertOutgoingMessage(ctx context.Context, userID, accountID int64, build func(outgoingmessage.Sender) (outgoingmessage.NewMessage, error)) (outgoingmessage.Message, error) {
	var saved outgoingmessage.Message
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the account, so a disconnect waits and then deletes the
		// message with it.
		account, err := queries.LockOutgoingMessageSender(ctx, sqlc.LockOutgoingMessageSenderParams{ID: accountID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return outgoingmessage.ErrAccountNotFound
		}
		if err != nil {
			return fmt.Errorf("lock sending account: %w", err)
		}

		// 2. Let build decide.
		sender := outgoingmessage.Sender{ConnectedAccountID: account.ID, Email: account.Email, NeedsReconnect: account.NeedsReconnect}
		next, err := build(sender)
		if err != nil {
			return err
		}

		// 3. Save the message.
		to, err := recipientsJSON(next.To)
		if err != nil {
			return err
		}
		cc, err := recipientsJSON(next.Cc)
		if err != nil {
			return err
		}
		bcc, err := recipientsJSON(next.Bcc)
		if err != nil {
			return err
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
			return fmt.Errorf("insert outgoing message: %w", err)
		}
		saved = outgoingmessage.Message{
			ID:                 row.ID,
			ConnectedAccountID: accountID,
			From:               next.From,
			Content:            next.Content,
			InbokMessageID:     next.InbokMessageID,
			Status:             outgoingmessage.Status(row.Status),
			CreatedAt:          row.CreatedAt,
		}
		return nil
	})
	if err != nil {
		return outgoingmessage.Message{}, err
	}
	return saved, nil
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
