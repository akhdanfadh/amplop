package search

import (
	"context"
	"fmt"
	"strings"
	"time"
	"uuid"

	"inbok/internal/mailsync"
	"inbok/internal/messagecontent"
)

// BatchSize is the most messages one [Indexer.Index] call should take.
//
// DESIGN: 100 documents, each with two body fields of at most maxBodyText,
// stay under about 13 MB, far below Meilisearch's 100 MB request limit,
// and their stored text is read in one call's time.
const BatchSize = 100

// Source is what storage holds of one message for its document.
type Source struct {
	ID           uuid.UUID
	AccountID    int64
	ThreadID     string // the provider's thread ID
	InternalDate time.Time
	Subject      string
	From         []mailsync.Address
	To           []mailsync.Address
	CC           []mailsync.Address
	MailboxIDs   []uuid.UUID // inbok's IDs of the mailboxes it is in
	// HasParts is whether content storage keeps the message's parts, so
	// its body has text to read.
	HasParts bool
}

// IndexStorage reads what documents are made of.
type IndexStorage interface {
	// IndexSources returns owner's messages among ids, in any order. An ID
	// owner has no message with, e.g. one deleted since, is left out.
	IndexSources(ctx context.Context, owner int64, ids []uuid.UUID) ([]Source, error)
}

// texts reads the stored text of a message's text parts, the ones a view
// shows apart from the rest, e.g. [messagecontent.Texts].
type texts interface {
	Read(ctx context.Context, accountID int64, messageID uuid.UUID, limits messagecontent.TextLimits) (messagecontent.MessageTexts, error)
}

// Indexer keeps a member's index in step with what storage holds.
type Indexer struct {
	index   Index
	storage IndexStorage
	texts   texts
}

// NewIndexer returns an indexer that writes to index what storage and
// texts hold.
func NewIndexer(index Index, storage IndexStorage, texts texts) *Indexer {
	return &Indexer{index: index, storage: storage, texts: texts}
}

// Index makes owner's messages with ids in the index what storage holds
// now: it upserts those storage has, and deletes the rest, e.g. messages
// deleted since. It returns [ErrNoOwner] before reading anything.
//
// DESIGN: it reads the messages when it runs rather than taking their
// documents from the change that queued it, so whichever call runs last
// writes the latest state. That holds only while one call at a time runs
// for an owner, as the search queue runs them (ADR005): the index orders
// no writes itself.
func (x *Indexer) Index(ctx context.Context, owner int64, ids []uuid.UUID) error {
	if err := ValidateOwner(owner); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	sources, err := x.storage.IndexSources(ctx, owner, ids)
	if err != nil {
		return fmt.Errorf("read messages to index: %w", err)
	}

	documents := make([]Document, 0, len(sources))
	found := make(map[uuid.UUID]bool, len(sources))
	for _, source := range sources {
		document, err := x.document(ctx, owner, source)
		if err != nil {
			return err
		}
		documents = append(documents, document)
		found[source.ID] = true
	}
	var gone []uuid.UUID
	for _, id := range ids {
		if !found[id] {
			gone = append(gone, id)
		}
	}

	if err := x.index.Upsert(ctx, documents); err != nil {
		return err
	}
	return x.index.Delete(ctx, owner, gone)
}

// RemoveAccount removes every message of owner's connected account
// accountID from the index. It returns [ErrNoOwner] before removing.
func (x *Indexer) RemoveAccount(ctx context.Context, owner, accountID int64) error {
	return x.index.DeleteAccount(ctx, owner, accountID)
}

// document returns owner's message source as the index holds it, with
// the text of its stored text parts.
func (x *Indexer) document(ctx context.Context, owner int64, source Source) (Document, error) {
	document := Document{
		ID:                 source.ID,
		OwnerUserID:        owner,
		ConnectedAccountID: source.AccountID,
		ThreadID:           source.ThreadID,
		InternalDate:       source.InternalDate,
		Subject:            source.Subject,
		From:               strings.Join(names(source.From), ", "),
		To:                 names(source.To),
		CC:                 names(source.CC),
		MailboxIDs:         source.MailboxIDs,
	}
	if len(source.From) > 0 {
		document.FromAddress = source.From[0].Address
	}
	if !source.HasParts {
		// Found by its headers until its body is stored (ADR005).
		return document, nil
	}
	// No more than BodyText reads of one field, shared by both, so the
	// store sends nothing it drops and a sender's message costs no more.
	texts, err := x.texts.Read(ctx, source.AccountID, source.ID, messagecontent.TextLimits{Parts: maxParts, Bytes: maxInput})
	if err != nil {
		return Document{}, fmt.Errorf("read body text: %w", err)
	}
	document.BodyText = BodyText(textParts(texts.Shown))
	document.AltText = BodyText(textParts(texts.Hidden))
	return document, nil
}

// textParts returns stored texts as [BodyText] reads them.
func textParts(texts []messagecontent.StoredText) []TextPart {
	parts := make([]TextPart, 0, len(texts))
	for _, text := range texts {
		parts = append(parts, TextPart{Format: format(text.MediaType), Content: string(text.Content)})
	}
	return parts
}

// names returns each address as a reader sees it: "Name <address>", or
// the address alone when it has no name.
func names(addresses []mailsync.Address) []string {
	out := make([]string, len(addresses))
	for n, a := range addresses {
		out[n] = a.Address
		if a.Name != "" {
			out[n] = a.Name + " <" + a.Address + ">"
		}
	}
	return out
}

// format returns the [TextFormat] of a text part's media type, or 0 for
// one [BodyText] does not read.
func format(mediaType string) TextFormat {
	switch mediaType {
	case "text/plain":
		return FormatPlain
	case "text/html":
		return FormatHTML
	}
	return 0
}
