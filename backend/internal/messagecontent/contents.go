package messagecontent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"
)

// ErrMessageNotFound means the account has no such synchronized message,
// e.g. synchronization deleted it while its content was fetched.
var ErrMessageNotFound = errors.New("messagecontent: message not found")

// saveLimit bounds [Contents.Save], inbok's choice: well inside the day
// that content no part names waits before [Cleanup] deletes it, so a save
// whose rows are slow to commit, e.g. behind a lock, does not name a blob
// the sweep deleted meanwhile.
const saveLimit = time.Hour

// Storage keeps the part rows that describe each message's stored
// content (ADR009, "What is stored").
type Storage interface {
	// SaveMessageParts saves root, each part with its [Part.BlobID], as the
	// part tree of account accountID's message messageID, unless the
	// message has parts already, which it keeps. It reports whether it
	// saved, and returns [ErrMessageNotFound] when the account has no such
	// message. In one transaction, it 1. locks the account's sync state, as
	// every write to the account's mail does first, so it never deadlocks
	// one; 2. checks that the message exists and has no parts; 3. inserts
	// the parts; 4. saves the job that indexes the message's body for
	// search, also when 2 found parts, as their content may just have been
	// stored again.
	SaveMessageParts(ctx context.Context, accountID int64, messageID uuid.UUID, root Part) (bool, error)
	// MessageParts returns the part tree of account accountID's message
	// messageID, and false when it has none, or no such message.
	MessageParts(ctx context.Context, accountID int64, messageID uuid.UUID) (Part, bool, error)
	// UnusedBlobs returns those of ids that no part names.
	UnusedBlobs(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error)
	// SavePartBlob names blobID as the content of the part at path of
	// account accountID's message messageID, unless the part names a blob
	// already, which it keeps, and returns the blob ID the part names. It
	// returns [ErrMessageNotFound] when the account is gone, and
	// [ErrPartNotFound] when its message has no part there. In one
	// transaction, it 1. locks the account's sync state, as every write to
	// the account's mail does first; 2. names the blob; 3. when it named the
	// content of a text part, saves the job that indexes the message's body
	// for search.
	SavePartBlob(ctx context.Context, accountID int64, messageID uuid.UUID, path []int, blobID uuid.UUID) (uuid.UUID, error)
}

// Contents keeps messages' content: the bytes in a [Store], and the part
// rows that name them in [Storage].
type Contents struct {
	store   Store
	storage Storage
}

// NewContents returns contents kept in store and storage.
func NewContents(store Store, storage Storage) *Contents {
	return &Contents{store: store, storage: storage}
}

// Save stores texts, the content of root's text parts as [Parse] returns
// them, then saves root as the part tree of account accountID's message
// messageID, each of those parts naming its content. Parts without a
// [Text], e.g. attachments, are saved unstored. A message whose parts are
// saved already keeps them, and its content is stored again under the IDs
// they name, which restores content the store lost. It gives up after an
// hour. It returns [ErrMessageNotFound] when the account has no such
// message, and the store's or storage's error.
//
// DESIGN: The store and PostgreSQL share no transaction, so content is
// stored before the row that names it: a row never names content that was
// never stored, and a failure leaves at worst content no row names, which
// [Cleanup] deletes a day later (ADR009, "Writing and deleting"). A retry
// before the rows commit stores the content again under new IDs: a job's
// step output may not hold mail, so the IDs could not be recorded before
// the fetch that tells how many there are.
func (c *Contents) Save(ctx context.Context, accountID int64, messageID uuid.UUID, root Part, texts []Text) error {
	ctx, cancel := context.WithTimeout(ctx, saveLimit)
	defer cancel()
	byPart := make(map[string][]byte, len(texts))
	for _, text := range texts {
		byPart[text.PartID] = text.Content
	}

	// 1. Learn the IDs saved parts name, so storing again restores their
	// content rather than leaving new copies no row names.
	saved, ok, err := c.storage.MessageParts(ctx, accountID, messageID)
	if err != nil {
		return fmt.Errorf("read message parts: %w", err)
	}
	ids := map[string]uuid.UUID{}
	if ok {
		storedIDs(saved, ids)
	}

	// 2. Store the content.
	stored, err := c.storeTexts(ctx, accountID, root, byPart, ids)
	if err != nil {
		return err
	}

	// 3. Save the rows naming it.
	if _, err := c.storage.SaveMessageParts(ctx, accountID, messageID, stored); err != nil {
		return fmt.Errorf("save message parts: %w", err)
	}
	return nil
}

// storeTexts stores the content in texts of part's text parts and those
// under it, by part ID, for accountID, each under its ID in ids or else a
// new one, and returns part with each of those naming its content.
func (c *Contents) storeTexts(ctx context.Context, accountID int64, part Part, texts map[string][]byte, ids map[string]uuid.UUID) (Part, error) {
	if content, ok := texts[part.ID]; ok && part.Kind == KindText {
		ref := Ref{Account: accountID, ID: uuid.New()}
		if id, ok := ids[part.ID]; ok {
			ref.ID = id
		}
		if err := c.store.Put(ctx, ref, bytes.NewReader(content)); err != nil {
			return Part{}, fmt.Errorf("store part %q: %w", part.ID, err)
		}
		part.BlobID = ref.ID
	}
	if part.Parts == nil {
		return part, nil
	}
	// A copy, so the caller's tree keeps its parts unstored.
	children := make([]Part, len(part.Parts))
	for i, child := range part.Parts {
		stored, err := c.storeTexts(ctx, accountID, child, texts, ids)
		if err != nil {
			return Part{}, err
		}
		children[i] = stored
	}
	part.Parts = children
	return part, nil
}

// storedIDs adds the blob ID of part, and of each part under it, that
// is stored to ids, by part ID.
func storedIDs(part Part, ids map[string]uuid.UUID) {
	if part.BlobID != uuid.Nil() {
		ids[part.ID] = part.BlobID
	}
	for _, child := range part.Parts {
		storedIDs(child, ids)
	}
}

// SavePart stores content as the content of part, the part at path of
// account accountID's message messageID, under the blob ID part names or
// else a new one, then names it in the part's row, and returns part naming
// the blob its row names. When another save named a blob first, the row
// keeps that one, and this content is left for [Cleanup]. It gives up
// after an hour. It returns [ErrMessageNotFound] when the account is gone,
// [ErrPartNotFound] when its message has no part there, and the store's or
// storage's error.
func (c *Contents) SavePart(ctx context.Context, accountID int64, messageID uuid.UUID, path []int, part Part, content []byte) (Part, error) {
	ctx, cancel := context.WithTimeout(ctx, saveLimit)
	defer cancel()

	// 1. Store the content first, as [Contents.Save] does.
	ref := Ref{Account: accountID, ID: part.BlobID}
	if ref.ID == uuid.Nil() {
		ref.ID = uuid.New()
	}
	if err := c.store.Put(ctx, ref, bytes.NewReader(content)); err != nil {
		return Part{}, fmt.Errorf("store part %q: %w", part.ID, err)
	}

	// 2. Name it in the part's row.
	named, err := c.storage.SavePartBlob(ctx, accountID, messageID, path, ref.ID)
	if err != nil {
		return Part{}, fmt.Errorf("save part blob: %w", err)
	}
	part.BlobID = named
	return part, nil
}
