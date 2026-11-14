package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"uuid"

	"github.com/jackc/pgx/v5"

	"inbok/internal/messagecontent"
	"inbok/internal/repository/postgres/sqlc"
)

// errPartIndex means a part has more parts than a path's integer holds.
var errPartIndex = errors.New("message part index is out of storage's range")

// SaveMessageParts saves root, each part with its blob ID, as the part
// tree of account accountID's message messageID, unless the message has
// parts already, which it keeps, and saves the job that indexes the
// message's body either way, in one transaction. It reports whether it
// saved parts, and returns [messagecontent.ErrMessageNotFound] when the
// account has no such message.
func (db *DB) SaveMessageParts(ctx context.Context, accountID int64, messageID uuid.UUID, root messagecontent.Part) (bool, error) {
	params, err := partParams(accountID, messageID, root)
	if err != nil {
		return false, err
	}
	saved := false
	err = pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the sync state, as every write to the account's mail does first
		// (ADR001, "Lock order"): a sync write deleting the message then waits, and
		// two saves of one message run one after the other.
		_, err := queries.LockSyncState(ctx, accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return messagecontent.ErrMessageNotFound
		}
		if err != nil {
			return fmt.Errorf("lock sync state: %w", err)
		}

		// 2. Keep parts saved before.
		hasParts, err := queries.MessageHasParts(ctx, sqlc.MessageHasPartsParams{ConnectedAccountID: accountID, ID: messageID})
		if errors.Is(err, pgx.ErrNoRows) {
			return messagecontent.ErrMessageNotFound
		}
		if err != nil {
			return fmt.Errorf("check message parts: %w", err)
		}
		if hasParts {
			// Content stored again under the IDs the parts name, e.g. after
			// the store lost it: its text may be back for the index.
			return db.indexAfter(ctx, tx, accountID, []uuid.UUID{messageID})
		}

		// 3. Insert the parts.
		var batchErr error
		queries.InsertMessagePart(ctx, params).Exec(func(_ int, err error) {
			batchErr = errors.Join(batchErr, err)
		})
		if batchErr != nil {
			return fmt.Errorf("insert message parts: %w", batchErr)
		}

		// 4. Save the job that indexes the message's body, so it commits
		// with the text it indexes (ADR005).
		if err := db.indexAfter(ctx, tx, accountID, []uuid.UUID{messageID}); err != nil {
			return err
		}
		saved = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return saved, nil
}

// partParams returns the insert parameters of root and every part under
// it, depth first, for accountID's message messageID. It returns
// [errPartIndex] for a part with more parts than a path holds.
func partParams(accountID int64, messageID uuid.UUID, root messagecontent.Part) ([]sqlc.InsertMessagePartParams, error) {
	var params []sqlc.InsertMessagePartParams
	var add func(part messagecontent.Part, path []int32) error
	add = func(part messagecontent.Part, path []int32) error {
		p := sqlc.InsertMessagePartParams{
			ConnectedAccountID: accountID,
			MessageID:          messageID,
			Path:               path,
			ProviderPartID:     part.ID,
			MediaType:          part.MediaType,
			Charset:            part.Charset,
			Filename:           part.Filename,
			Size:               part.Size,
			Disposition:        string(part.Disposition),
			MimeContentID:      part.ContentID,
			RelatedStart:       part.Start,
			Kind:               string(part.Kind),
		}
		if part.BlobID != uuid.Nil() {
			blob := part.BlobID
			p.BlobID = &blob
		}
		params = append(params, p)
		if len(part.Parts) > math.MaxInt32 {
			return errPartIndex
		}
		for i, child := range part.Parts {
			// A copy, so siblings' paths share no array.
			if err := add(child, append(slices.Clone(path), int32(i))); err != nil {
				return err
			}
		}
		return nil
	}
	if err := add(root, []int32{}); err != nil {
		return nil, err
	}
	return params, nil
}

// MessageParts returns the part tree of account accountID's message
// messageID, and false when it has none, or no such message.
func (db *DB) MessageParts(ctx context.Context, accountID int64, messageID uuid.UUID) (messagecontent.Part, bool, error) {
	rows, err := sqlc.New(db.pool).ListMessageParts(ctx, sqlc.ListMessagePartsParams{ConnectedAccountID: accountID, MessageID: messageID})
	if err != nil {
		return messagecontent.Part{}, false, fmt.Errorf("list message parts: %w", err)
	}
	if len(rows) == 0 {
		return messagecontent.Part{}, false, nil
	}
	return partTree(rows), true, nil
}

// partNode is a part while its tree is rebuilt from rows.
type partNode struct {
	part     messagecontent.Part
	children []*partNode
}

// partTree returns the tree of rows, which list a message's parts depth
// first with the top part, whose path is empty, first. A row whose parent
// is not listed before it is left out.
func partTree(rows []sqlc.ListMessagePartsRow) messagecontent.Part {
	nodes := make(map[string]*partNode, len(rows))
	var root *partNode
	for _, row := range rows {
		node := &partNode{part: partFromRow(row)}
		key := fmt.Sprint(row.Path)
		if len(row.Path) == 0 {
			root = node
			nodes[key] = node
			continue
		}
		parent, ok := nodes[fmt.Sprint(row.Path[:len(row.Path)-1])]
		if !ok {
			continue
		}
		parent.children = append(parent.children, node)
		nodes[key] = node
	}
	if root == nil {
		return messagecontent.Part{}
	}
	return root.tree()
}

// tree returns n's part with its parts.
func (n *partNode) tree() messagecontent.Part {
	part := n.part
	for _, child := range n.children {
		part.Parts = append(part.Parts, child.tree())
	}
	return part
}

// partFromRow converts a row to a part without its parts.
func partFromRow(row sqlc.ListMessagePartsRow) messagecontent.Part {
	part := messagecontent.Part{
		ID:          row.ProviderPartID,
		MediaType:   row.MediaType,
		Charset:     row.Charset,
		Filename:    row.Filename,
		Size:        row.Size,
		Disposition: messagecontent.Disposition(row.Disposition),
		ContentID:   row.MimeContentID,
		Kind:        messagecontent.Kind(row.Kind),
		Start:       row.RelatedStart,
	}
	if row.BlobID != nil {
		part.BlobID = *row.BlobID
	}
	return part
}

// UnusedBlobs returns those of ids that no part names.
func (db *DB) UnusedBlobs(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	unused, err := sqlc.New(db.pool).ListUnusedBlobs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list unused blobs: %w", err)
	}
	return unused, nil
}

// deleteBlobsAfter saves, in tx, the job that deletes accountID's blobs
// with blobIDs, the content of messages tx deletes, once tx commits. It
// saves nothing for no IDs.
func (db *DB) deleteBlobsAfter(ctx context.Context, tx pgx.Tx, accountID int64, blobIDs []uuid.UUID) error {
	if len(blobIDs) == 0 {
		return nil
	}
	if db.workflows == nil {
		return errNoWorkflows
	}
	if err := db.workflows.EnqueueContentDeletion(ctx, tx, accountID, blobIDs); err != nil {
		return fmt.Errorf("save content deletion: %w", err)
	}
	return nil
}

// MessageProviderID returns the provider's ID of account accountID's
// message messageID, or [messagecontent.ErrMessageNotFound] when there is
// no such message.
func (db *DB) MessageProviderID(ctx context.Context, accountID int64, messageID uuid.UUID) (string, error) {
	id, err := sqlc.New(db.pool).GetMessageProviderID(ctx, sqlc.GetMessageProviderIDParams{ConnectedAccountID: accountID, ID: messageID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", messagecontent.ErrMessageNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read message provider id: %w", err)
	}
	return id, nil
}

// UserMessageAccount returns the connected account of user userID's
// message messageID, or [messagecontent.ErrMessageNotFound] when none of
// the user's accounts has it.
func (db *DB) UserMessageAccount(ctx context.Context, userID int64, messageID uuid.UUID) (int64, error) {
	accountID, err := sqlc.New(db.pool).GetUserMessageAccount(ctx, sqlc.GetUserMessageAccountParams{ID: messageID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, messagecontent.ErrMessageNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read message account: %w", err)
	}
	return accountID, nil
}

// SavePartBlob names blobID as the content of the part at path of account
// accountID's message messageID, unless the part names a blob already,
// which it keeps, and returns the blob ID the part names, in one
// transaction that first locks the account's sync state. When it names
// the content of a text part, it saves the job that indexes the message's
// body with it. It returns
// [messagecontent.ErrMessageNotFound] when the account is gone, and
// [messagecontent.ErrPartNotFound] when its message has no part there.
func (db *DB) SavePartBlob(ctx context.Context, accountID int64, messageID uuid.UUID, path []int, blobID uuid.UUID) (uuid.UUID, error) {
	stored := make([]int32, len(path))
	for i, index := range path {
		// A path past storage's range names no saved part.
		if index < 0 || index > math.MaxInt32 {
			return uuid.Nil(), messagecontent.ErrPartNotFound
		}
		stored[i] = int32(index)
	}
	var named uuid.UUID
	err := db.withSyncStateLock(ctx, accountID, func(tx pgx.Tx) error {
		// 1. Name the blob, unless the part names one already.
		row, err := sqlc.New(tx).SetMessagePartBlob(ctx, sqlc.SetMessagePartBlobParams{
			BlobID: blobID, ConnectedAccountID: accountID, MessageID: messageID, Path: stored,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return messagecontent.ErrPartNotFound
		}
		if err != nil {
			return fmt.Errorf("set message part blob: %w", err)
		}
		// The update sets one when none was, so the row names one now.
		named = *row.BlobID

		// 2. Save the job that indexes the text it named, so the job commits
		// with the row it reads: a text body the provider sent apart from the
		// message, e.g. Gmail's long HTML, joins the body text the index
		// holds.
		if messagecontent.Kind(row.Kind) != messagecontent.KindText || named != blobID {
			return nil
		}
		return db.indexAfter(ctx, tx, accountID, []uuid.UUID{messageID})
	})
	return named, err
}

// withSyncStateLock runs write in a transaction that first locks account
// accountID's sync state. It returns [messagecontent.ErrMessageNotFound]
// when the account has no sync state, and write's error.
func (db *DB) withSyncStateLock(ctx context.Context, accountID int64, write func(tx pgx.Tx) error) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the sync state, as every write to the account's mail does
		// first (ADR001, "Lock order"), so a sync write waits, rather than
		// deadlocks, on the rows the write takes after it.
		_, err := queries.LockSyncState(ctx, accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return messagecontent.ErrMessageNotFound
		}
		if err != nil {
			return fmt.Errorf("lock sync state: %w", err)
		}

		// 2. Write.
		return write(tx)
	})
}
