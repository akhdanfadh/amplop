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
// parts already, which it keeps, in one transaction. It reports whether it
// saved, and returns [messagecontent.ErrMessageNotFound] when the account
// has no such message.
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
			return nil
		}

		// 3. Insert the parts.
		var batchErr error
		queries.InsertMessagePart(ctx, params).Exec(func(_ int, err error) {
			batchErr = errors.Join(batchErr, err)
		})
		if batchErr != nil {
			return fmt.Errorf("insert message parts: %w", batchErr)
		}

		// 4. Search indexing (ADR005, branch 6b) saves its job for the
		// message here, so the job commits with the text it indexes.
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
