package meilisearch

import (
	"context"
	"uuid"

	meili "github.com/meilisearch/meilisearch-go"

	"inbok/internal/search"
)

// document is a [search.Document] as Meilisearch stores it.
//
// DESIGN: user and account IDs are decimal strings, not numbers.
// Meilisearch keeps a number as a 64-bit float, which cannot tell apart
// IDs above 2^53, e.g. 2^53 and 2^53 + 1, so a filter on one would match
// the other. A string matches exactly.
type document struct {
	ID                 string `json:"id"`
	OwnerUserID        string `json:"owner_user_id"`
	ConnectedAccountID string `json:"connected_account_id"`
	// Unix milliseconds, which filters and ranks as a number: Gmail's own
	// precision, so a range or the newest of a conversation is exact.
	// They stay exact, as a 64-bit float holds every integer up to 2^53,
	// some 285,000 years of them.
	InternalDate  int64    `json:"internal_date"`
	Subject       string   `json:"subject"`
	From          string   `json:"from"`
	To            []string `json:"to"`
	CC            []string `json:"cc"`
	BodyText      string   `json:"body_text"`
	AltText       string   `json:"alt_text"`
	FromAddresses []string `json:"from_addresses"`
	HasAttachment bool     `json:"has_attachment"`
	MailboxIDs    []string `json:"mailbox_ids"`
	Conversation  string   `json:"conversation"`
}

// Upsert adds documents to their owners' indexes, replacing whole any with
// the same ID, and waits until Meilisearch has applied it. It returns
// [search.ErrNoOwner] before writing any. When an owner's write fails,
// the owners before it are written already; repeating the call is safe.
func (i *Index) Upsert(ctx context.Context, documents []search.Document) error {
	if err := search.ValidateDocuments(documents); err != nil {
		return err
	}
	var owners []int64
	rows := map[int64][]document{}
	for _, d := range documents {
		if _, ok := rows[d.OwnerUserID]; !ok {
			owners = append(owners, d.OwnerUserID)
		}
		rows[d.OwnerUserID] = append(rows[d.OwnerUserID], document{
			ID:                 d.ID.String(),
			OwnerUserID:        decimal(d.OwnerUserID),
			ConnectedAccountID: decimal(d.ConnectedAccountID),
			InternalDate:       d.InternalDate.UnixMilli(),
			Subject:            d.Subject,
			From:               d.From,
			To:                 d.To,
			CC:                 d.CC,
			BodyText:           d.BodyText,
			AltText:            d.AltText,
			FromAddresses:      nonNil(d.FromAddresses),
			HasAttachment:      d.HasAttachment,
			MailboxIDs:         texts(d.MailboxIDs),
			Conversation:       conversation(d.ConnectedAccountID, d.ThreadID),
		})
	}
	for _, owner := range owners {
		if err := i.upsert(ctx, i.uid(owner), rows[owner]); err != nil {
			return err
		}
	}
	return nil
}

// conversation returns the conversation key of a message in account's
// thread: the account's ID, a slash, and the thread ID. No two
// conversations share one, as an account's ID holds no slash.
func conversation(account int64, thread string) string {
	return decimal(account) + "/" + thread
}

// nonNil returns values, or an empty list for none, so a document holds
// a list, not null.
func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// texts returns ids as Meilisearch stores them: canonical text, which a
// filter matches exactly. It returns an empty list for none, not null.
func texts(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for n, id := range ids {
		out[n] = id.String()
	}
	return out
}

// upsert applies the index uid's settings, then adds rows to it, waiting
// until Meilisearch has applied both.
func (i *Index) upsert(ctx context.Context, uid string, rows []document) error {
	// 1. Apply the settings before the documents, every time. A missing
	// index, e.g. at a member's first write, is created with them, so no
	// search ever meets its documents in an index without them, which
	// refuses the filters every search uses. No delete of the whole index
	// falls between the steps: a rebuild's, the only one, runs in the
	// member's partition of the search queue, as their writes do.
	task, err := i.apply(ctx, uid)
	if err := i.wait(ctx, "apply search settings", task, err); err != nil {
		return err
	}

	// 2. Add the documents. Meilisearch would guess the primary key only
	// from a document with exactly one field ending in "id", and these have
	// three. AddDocuments replaces a document; UpdateDocuments would merge
	// into it and keep a mailbox the message left.
	key := fieldID
	task, err = i.client.Index(uid).AddDocumentsWithContext(ctx, rows, &meili.DocumentOptions{PrimaryKey: &key})
	return i.wait(ctx, "upsert search documents", task, err)
}

// Delete removes owner's documents with these message IDs and waits until
// Meilisearch has applied it. It returns [search.ErrNoOwner] before
// deleting.
func (i *Index) Delete(ctx context.Context, owner int64, ids []uuid.UUID) error {
	if err := search.ValidateOwner(owner); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	keys := make([]string, len(ids))
	for n, id := range ids {
		keys[n] = id.String()
	}
	task, err := i.client.Index(i.uid(owner)).DeleteDocumentsWithContext(ctx, keys, nil)
	return ignoreMissing(i.wait(ctx, "delete search documents", task, err))
}

// DeleteAccount removes every document of owner's connected account and
// waits until Meilisearch has applied it. It returns [search.ErrNoOwner]
// before deleting.
func (i *Index) DeleteAccount(ctx context.Context, owner, accountID int64) error {
	if err := search.ValidateOwner(owner); err != nil {
		return err
	}
	// The owner, as in every search, in case a document reached the wrong
	// index.
	filter := []string{
		fieldOwner + " = " + quote(decimal(owner)),
		fieldAccount + " = " + quote(decimal(accountID)),
	}
	task, err := i.client.Index(i.uid(owner)).DeleteDocumentsByFilterWithContext(ctx, filter, nil)
	return ignoreMissing(i.wait(ctx, "delete an account's search documents", task, err))
}
