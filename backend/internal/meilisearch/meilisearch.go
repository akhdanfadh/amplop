// Package meilisearch implements [search.Index] on Meilisearch Community
// Edition, with its official Go client, meilisearch-go.
package meilisearch

import (
	"strconv"

	meili "github.com/meilisearch/meilisearch-go"
)

const (
	// indexPrefix starts the name of each member's index, which ends with
	// the member's user ID, e.g. messages_42.
	indexPrefix = "messages_"

	// listLimit is how many indexes [Index.Prepare] asks for at a time.
	listLimit = 100

	// documentPage is how many document IDs [Index.DocumentIDs] asks for at
	// a time: IDs alone, so a page stays small.
	documentPage = 1000
)

// The index's fields.
const (
	fieldID        = "id"
	fieldOwner     = "owner_user_id"
	fieldAccount   = "connected_account_id"
	fieldSubject   = "subject"
	fieldFrom      = "from"
	fieldTo        = "to"
	fieldCC        = "cc"
	fieldBodyText  = "body_text"
	fieldAltText   = "alt_text"
	fieldMailboxes = "mailbox_ids"
	// The message's conversation: its account and the provider's thread
	// ID, which a search returns one message of.
	fieldConversation = "conversation"
	fieldDate         = "internal_date"
	// Every address of the message's From, as storage holds it.
	fieldFromAddresses = "from_addresses"
	fieldAttachment    = "has_attachment"
	// The mailboxes of the message's conversation, and whether it is
	// starred: where lists place the conversation.
	fieldConversationMailboxes = "conversation_mailbox_ids"
	fieldConversationFlagged   = "conversation_flagged"
)

// Index is the message index on one Meilisearch server: one Meilisearch
// index per member. It is safe for concurrent use.
//
// DESIGN: a member's documents live in an index of their own, so a call
// that forgets a filter, or a filter that is wrong, still reaches only the
// one member's documents, and an unset owner names an index that does not
// exist. It cannot catch a caller that names another member as owner:
// that member's index answers, so the caller passes the signed-in member.
// The cost is settings applied to every member's index: at startup, and
// before every upsert, which may create the index. Searches and
// account deletes also filter on the owner inside each index, as a second
// guard, should a document ever reach the wrong one.
type Index struct {
	client       meili.ServiceManager
	prefix       string
	listLimit    int64
	documentPage int64
	settings     meili.Settings
}

// New returns the index on the Meilisearch server at url, e.g.
// http://meilisearch:7700, signing requests with key. It connects on the
// first call.
func New(url, key string) *Index {
	// NOTE: retries are off because of two bugs in meilisearch-go v0.36.3,
	// both fixed on its main branch but in no release yet. A retry resends
	// the request without its body, which the first attempt used up
	// (https://github.com/meilisearch/meilisearch-go/pull/798), and a
	// success on the last retry is reported as MaxRetriesExceeded
	// (https://github.com/meilisearch/meilisearch-go/issues/816, fixed by
	// https://github.com/meilisearch/meilisearch-go/pull/817). Turn retries
	// back on with a release that has both. Until then, every method is
	// safe to repeat, so a caller that wants a retry calls it again.
	client := meili.New(url, meili.WithAPIKey(key), meili.DisableRetries())
	return &Index{client: client, prefix: indexPrefix, listLimit: listLimit, documentPage: documentPage, settings: indexSettings()}
}

// uid returns the name of owner's index.
func (i *Index) uid(owner int64) string {
	return i.prefix + decimal(owner)
}

// decimal returns id as the string the index stores it as.
func decimal(id int64) string {
	return strconv.FormatInt(id, 10)
}
