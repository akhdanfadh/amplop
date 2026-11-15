package searchtest

import (
	"context"
	"math"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/search"
)

// Members and accounts in the contract's documents.
const (
	asha        int64 = 1
	ashaWork    int64 = 11
	ashaHome    int64 = 12
	budi        int64 = 2
	budiAccount int64 = 21
)

// Mailboxes of Asha's work account, by inbok's IDs.
var (
	inboxBox   = uuid.MustParse("019a0000-0000-7000-8000-0000000000a1")
	spamBox    = uuid.MustParse("019a0000-0000-7000-8000-0000000000a2")
	trashBox   = uuid.MustParse("019a0000-0000-7000-8000-0000000000a3")
	clientsBox = uuid.MustParse("019a0000-0000-7000-8000-0000000000a4")
)

// firstPage is a page large enough for every search in the contract.
var firstPage = search.Query{Page: 1, PageSize: 20}

// RunContract tests the behaviour every [search.Index] shares: owners, account
// and mailbox filters, which fields a search matches, every word of a query,
// one message per conversation, where it matched, replacing, deleting within
// one owner's documents, listing and deleting owners and their document IDs,
// paging, cancellation, and refused calls. open returns
// a new, prepared index for one test. Text matching beyond whole words, e.g.
// typos, is the real index's own and tested there.
func RunContract(t *testing.T, open func(t *testing.T) search.Index) {
	t.Run("owner", func(t *testing.T) {
		index := open(t)
		mine := document(asha, ashaWork, "invoice")
		theirs := document(budi, budiAccount, "invoice")
		upsert(t, index, mine, theirs)

		plain := find(t, index, asha, query("invoice"))
		// Budi's account ID asks for Budi's mail, which Asha never gets.
		foreign := query("invoice")
		foreign.Accounts = []int64{budiAccount}
		named := find(t, index, asha, foreign)
		foreign.Accounts = []int64{ashaWork, budiAccount}
		mixed := find(t, index, asha, foreign)

		assert.Equal(t, foundIDs{IDs: []uuid.UUID{mine.ID}, Total: 1}, plain)
		assert.Empty(t, named.IDs)
		assert.Equal(t, 0, named.Total)
		assert.Equal(t, foundIDs{IDs: []uuid.UUID{mine.ID}, Total: 1}, mixed)
	})

	t.Run("large IDs", func(t *testing.T) {
		// Two IDs a 64-bit float, which JSON numbers often become, cannot
		// tell apart: 2^53 and 2^53 + 1.
		const first, second int64 = 1 << 53, 1<<53 + 1
		mine := document(first, first, "invoice")
		theirs := document(second, second, "invoice")
		index := open(t)
		upsert(t, index, mine, theirs)

		found := find(t, index, first, query("invoice"))
		require.NoError(t, index.DeleteAccount(t.Context(), second, second))
		kept := find(t, index, first, query("invoice"))

		assert.Equal(t, foundIDs{IDs: []uuid.UUID{mine.ID}, Total: 1}, found)
		assert.Equal(t, foundIDs{IDs: []uuid.UUID{mine.ID}, Total: 1}, kept)
	})

	t.Run("accounts", func(t *testing.T) {
		index := open(t)
		work := document(asha, ashaWork, "invoice")
		home := document(asha, ashaHome, "invoice")
		upsert(t, index, work, home)

		q := query("invoice")
		q.Accounts = []int64{ashaHome}
		result := find(t, index, asha, q)

		assert.Equal(t, []uuid.UUID{home.ID}, result.IDs)
	})

	t.Run("exclude mailboxes", func(t *testing.T) {
		index := open(t)
		inbox := document(asha, ashaWork, "invoice")
		inbox.MailboxIDs = []uuid.UUID{inboxBox, clientsBox}
		// In the inbox and Spam at once: one excluded mailbox leaves it out.
		spam := document(asha, ashaWork, "invoice")
		spam.MailboxIDs = []uuid.UUID{inboxBox, spamBox}
		trash := document(asha, ashaWork, "invoice")
		trash.MailboxIDs = []uuid.UUID{trashBox}
		// In no mailbox, e.g. archived in Gmail.
		none := document(asha, ashaWork, "invoice")
		upsert(t, index, inbox, spam, trash, none)

		q := query("invoice")
		q.ExcludeMailboxes = []uuid.UUID{spamBox, trashBox}
		result := find(t, index, asha, q)

		assert.ElementsMatch(t, []uuid.UUID{inbox.ID, none.ID}, result.IDs)
	})

	t.Run("cancelled", func(t *testing.T) {
		index := open(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		added := document(asha, ashaWork, "invoice")

		_, searchErr := index.Search(ctx, asha, query("invoice"))
		_, ownersErr := index.Owners(ctx)
		_, idsErr := index.DocumentIDs(ctx, asha)
		errs := map[string]error{
			"prepare":        index.Prepare(ctx),
			"upsert":         index.Upsert(ctx, []search.Document{added}),
			"delete":         index.Delete(ctx, asha, []uuid.UUID{added.ID}),
			"delete account": index.DeleteAccount(ctx, asha, ashaWork),
			"search":         searchErr,
			"owners":         ownersErr,
			"document IDs":   idsErr,
			"delete owner":   index.DeleteOwner(ctx, asha),
		}
		result := find(t, index, asha, query("invoice"))

		for name, err := range errs {
			require.ErrorIs(t, err, context.Canceled, name)
		}
		assert.Empty(t, result.IDs)
	})

	t.Run("fields", func(t *testing.T) {
		index := open(t)
		subject := document(asha, ashaWork, "invoice")
		from := document(asha, ashaWork, "hello")
		from.From = "Citra <citra@example.com>"
		to := document(asha, ashaWork, "hello")
		to.To = []string{"Dewi <dewi@example.com>"}
		cc := document(asha, ashaWork, "hello")
		cc.CC = []string{"Eka <eka@example.com>"}
		body := document(asha, ashaWork, "hello")
		body.BodyText = "the quarterly report"
		alt := document(asha, ashaWork, "hello")
		alt.AltText = "the annual budget"
		upsert(t, index, subject, from, to, cc, body, alt)

		cases := map[string]uuid.UUID{"invoice": subject.ID, "citra": from.ID, "dewi": to.ID, "eka": cc.ID, "quarterly": body.ID, "annual": alt.ID}
		for word, want := range cases {
			result := find(t, index, asha, query(word))
			assert.Equal(t, []uuid.UUID{want}, result.IDs, word)
		}
	})

	t.Run("every word", func(t *testing.T) {
		index := open(t)
		both := document(asha, ashaWork, "invoice from budi")
		one := document(asha, ashaWork, "invoice from citra")
		upsert(t, index, both, one)

		result := find(t, index, asha, query("invoice budi"))

		assert.Equal(t, foundIDs{IDs: []uuid.UUID{both.ID}, Total: 1}, result)
	})

	t.Run("one per conversation", func(t *testing.T) {
		index := open(t)
		// The newer of two equal matches is the conversation's best, though
		// written second.
		older := document(asha, ashaWork, "invoice")
		newer := document(asha, ashaWork, "invoice")
		newer.ThreadID = older.ThreadID
		newer.InternalDate = older.InternalDate.Add(time.Hour)
		// The same thread ID in another account is another conversation.
		home := document(asha, ashaHome, "invoice")
		home.ThreadID = older.ThreadID
		upsert(t, index, older, newer, home)

		result := find(t, index, asha, query("invoice"))

		assert.ElementsMatch(t, []uuid.UUID{newer.ID, home.ID}, result.IDs)
		assert.Equal(t, 2, result.Total)
	})

	t.Run("conversation without its excluded messages", func(t *testing.T) {
		index := open(t)
		trashed := document(asha, ashaWork, "invoice")
		trashed.MailboxIDs = []uuid.UUID{trashBox}
		kept := document(asha, ashaWork, "invoice")
		kept.ThreadID = trashed.ThreadID
		kept.InternalDate = trashed.InternalDate.Add(-time.Hour)
		upsert(t, index, trashed, kept)

		q := query("invoice")
		q.ExcludeMailboxes = []uuid.UUID{trashBox}
		result := find(t, index, asha, q)

		assert.Equal(t, foundIDs{IDs: []uuid.UUID{kept.ID}, Total: 1}, result)
	})

	t.Run("matches", func(t *testing.T) {
		index := open(t)
		// Bytes, not characters, before the match: é and the dash take more
		// than one each.
		both := document(asha, ashaWork, "Café — invoice")
		both.BodyText = "Please pay the invoice today."
		subject := document(asha, ashaWork, "Paid invoice")
		subject.BodyText = "Thank you."
		upsert(t, index, both, subject)

		result := hits(t, index, asha, query("invoice"))
		byID := map[uuid.UUID]search.Hit{}
		for _, hit := range result.Hits {
			byID[hit.ID] = hit
		}
		inBoth, inSubject := byID[both.ID], byID[subject.ID]

		assert.Equal(t, both.Subject, inBoth.Subject)
		assert.Equal(t, []search.Span{{Start: len("Café — "), Length: len("invoice")}}, inBoth.SubjectMatches)
		require.Len(t, inBoth.ExcerptMatches, 1)
		match := inBoth.ExcerptMatches[0]
		assert.Equal(t, "invoice", inBoth.Excerpt[match.Start:match.Start+match.Length])
		assert.Contains(t, inBoth.Excerpt, "pay the invoice today")
		assert.Equal(t, []search.Span{{Start: len("Paid "), Length: len("invoice")}}, inSubject.SubjectMatches)
		assert.Empty(t, inSubject.Excerpt)
		assert.Empty(t, inSubject.ExcerptMatches)
	})

	t.Run("empty text", func(t *testing.T) {
		index := open(t)
		first := document(asha, ashaWork, "invoice")
		second := document(asha, ashaWork, "hello")
		upsert(t, index, first, second, document(budi, budiAccount, "invoice"))

		result := find(t, index, asha, query(""))

		assert.ElementsMatch(t, []uuid.UUID{first.ID, second.ID}, result.IDs)
	})

	t.Run("upsert replaces", func(t *testing.T) {
		index := open(t)
		before := document(asha, ashaWork, "invoice")
		before.MailboxIDs = []uuid.UUID{spamBox}
		upsert(t, index, before)

		after := before
		after.Subject = "receipt"
		after.MailboxIDs = []uuid.UUID{inboxBox}
		upsert(t, index, after)
		upsert(t, index, after)
		old := find(t, index, asha, query("invoice"))
		q := query("receipt")
		q.ExcludeMailboxes = []uuid.UUID{spamBox}
		current := find(t, index, asha, q)

		assert.Equal(t, 0, old.Total)
		assert.Equal(t, foundIDs{IDs: []uuid.UUID{after.ID}, Total: 1}, current)
	})

	t.Run("upsert copies", func(t *testing.T) {
		index := open(t)
		spam := document(asha, ashaWork, "invoice")
		spam.MailboxIDs = []uuid.UUID{spamBox}
		upsert(t, index, spam)

		// The caller reuses its slice after the upsert.
		spam.MailboxIDs[0] = inboxBox
		q := query("invoice")
		q.ExcludeMailboxes = []uuid.UUID{spamBox}
		result := find(t, index, asha, q)

		assert.Empty(t, result.IDs)
	})

	t.Run("delete", func(t *testing.T) {
		index := open(t)
		kept := document(asha, ashaWork, "invoice")
		first := document(asha, ashaWork, "invoice")
		second := document(asha, ashaWork, "invoice")
		upsert(t, index, kept, first, second)

		// One ID the index never had.
		ids := []uuid.UUID{first.ID, second.ID, uuid.New()}
		require.NoError(t, index.Delete(t.Context(), asha, ids))
		require.NoError(t, index.Delete(t.Context(), asha, ids))
		result := find(t, index, asha, query("invoice"))

		assert.Equal(t, []uuid.UUID{kept.ID}, result.IDs)
	})

	t.Run("delete account", func(t *testing.T) {
		index := open(t)
		work := document(asha, ashaWork, "invoice")
		home := document(asha, ashaHome, "invoice")
		upsert(t, index, work, home)

		require.NoError(t, index.DeleteAccount(t.Context(), asha, ashaWork))
		require.NoError(t, index.DeleteAccount(t.Context(), asha, ashaWork))
		result := find(t, index, asha, query("invoice"))

		assert.Equal(t, []uuid.UUID{home.ID}, result.IDs)
	})

	t.Run("one ID, two owners", func(t *testing.T) {
		index := open(t)
		mine := document(asha, ashaWork, "invoice")
		theirs := mine
		theirs.OwnerUserID = budi
		theirs.ConnectedAccountID = budiAccount
		upsert(t, index, mine)
		upsert(t, index, theirs)

		require.NoError(t, index.Delete(t.Context(), budi, []uuid.UUID{theirs.ID}))
		kept := find(t, index, asha, query("invoice"))
		gone := find(t, index, budi, query("invoice"))

		assert.Equal(t, []uuid.UUID{mine.ID}, kept.IDs)
		assert.Empty(t, gone.IDs)
	})

	t.Run("deletes stay with the owner", func(t *testing.T) {
		index := open(t)
		theirs := document(budi, budiAccount, "invoice")
		upsert(t, index, document(asha, ashaWork, "invoice"), theirs)

		// Asha names Budi's message and Budi's account.
		require.NoError(t, index.Delete(t.Context(), asha, []uuid.UUID{theirs.ID}))
		require.NoError(t, index.DeleteAccount(t.Context(), asha, budiAccount))
		result := find(t, index, budi, query("invoice"))

		assert.Equal(t, []uuid.UUID{theirs.ID}, result.IDs)
	})

	t.Run("nothing indexed yet", func(t *testing.T) {
		index := open(t)
		upsert(t, index, document(asha, ashaWork, "invoice"))

		// Budi has never had a document indexed.
		result := find(t, index, budi, query("invoice"))
		deleted := index.Delete(t.Context(), budi, []uuid.UUID{uuid.New()})
		deletedAccount := index.DeleteAccount(t.Context(), budi, budiAccount)

		assert.Empty(t, result.IDs)
		assert.Equal(t, 0, result.Total)
		require.NoError(t, deleted)
		require.NoError(t, deletedAccount)
	})

	t.Run("pages", func(t *testing.T) {
		index := open(t)
		all := []search.Document{
			document(asha, ashaWork, "invoice"),
			document(asha, ashaWork, "invoice"),
			document(asha, ashaWork, "invoice"),
		}
		upsert(t, index, all...)

		q := search.Query{Text: "invoice", Page: 1, PageSize: 2}
		first := find(t, index, asha, q)
		q.Page = 2
		second := find(t, index, asha, q)
		q.Page = 3
		past := find(t, index, asha, q)
		q.Page = math.MaxInt
		far := find(t, index, asha, q)
		// (Page - 1) * PageSize overflows a 64-bit offset.
		overflow := find(t, index, asha, search.Query{Text: "invoice", Page: 1<<62 + 1, PageSize: 4})
		huge := find(t, index, asha, search.Query{Text: "invoice", Page: math.MaxInt, PageSize: math.MaxInt})
		whole := find(t, index, asha, search.Query{Text: "invoice", Page: 1, PageSize: math.MaxInt})

		assert.Len(t, first.IDs, 2)
		assert.Len(t, second.IDs, 1)
		assert.ElementsMatch(t, []uuid.UUID{all[0].ID, all[1].ID, all[2].ID}, append(first.IDs, second.IDs...))
		assert.Empty(t, past.IDs)
		assert.Empty(t, far.IDs)
		assert.Empty(t, overflow.IDs)
		assert.Empty(t, huge.IDs)
		assert.Len(t, whole.IDs, 3)
		totals := []int{first.Total, second.Total, past.Total, far.Total, overflow.Total, huge.Total, whole.Total}
		assert.Equal(t, []int{3, 3, 3, 3, 3, 3, 3}, totals)
	})

	t.Run("most counted", func(t *testing.T) {
		index := open(t)
		all := make([]search.Document, search.MaxTotal+3)
		for n := range all {
			all[n] = document(asha, ashaWork, "invoice")
		}
		upsert(t, index, all...)

		result := find(t, index, asha, search.Query{Text: "invoice", Page: 1, PageSize: 10})

		assert.Len(t, result.IDs, 10)
		assert.Equal(t, search.MaxTotal, result.Total)
	})

	t.Run("refused", func(t *testing.T) {
		index := open(t)
		ids := []uuid.UUID{uuid.New()}

		_, noOwner := index.Search(t.Context(), 0, query("invoice"))
		_, noPage := index.Search(t.Context(), asha, search.Query{Text: "invoice", PageSize: 20})
		_, noSize := index.Search(t.Context(), asha, search.Query{Text: "invoice", Page: 1})
		noDeleteOwner := index.Delete(t.Context(), 0, ids)
		noAccountOwner := index.DeleteAccount(t.Context(), 0, ashaWork)
		_, noListOwner := index.DocumentIDs(t.Context(), 0)
		noRemovedOwner := index.DeleteOwner(t.Context(), 0)
		unowned := document(0, ashaWork, "invoice")
		noUpsertOwner := index.Upsert(t.Context(), []search.Document{document(asha, ashaWork, "invoice"), unowned})
		stored := find(t, index, asha, query("invoice"))

		require.ErrorIs(t, noOwner, search.ErrNoOwner)
		require.ErrorIs(t, noPage, search.ErrBadPage)
		require.ErrorIs(t, noSize, search.ErrBadPage)
		require.ErrorIs(t, noDeleteOwner, search.ErrNoOwner)
		require.ErrorIs(t, noAccountOwner, search.ErrNoOwner)
		require.ErrorIs(t, noListOwner, search.ErrNoOwner)
		require.ErrorIs(t, noRemovedOwner, search.ErrNoOwner)
		// A batch with one unowned document writes none of it.
		require.ErrorIs(t, noUpsertOwner, search.ErrNoOwner)
		assert.Empty(t, stored.IDs)
	})

	t.Run("nothing to write", func(t *testing.T) {
		index := open(t)

		require.NoError(t, index.Upsert(t.Context(), nil))
		require.NoError(t, index.Delete(t.Context(), asha, nil))
	})

	t.Run("owners", func(t *testing.T) {
		index := open(t)
		mine := document(asha, ashaWork, "invoice")
		theirs := document(budi, budiAccount, "invoice")
		upsert(t, index, mine, theirs)

		// An owner whose documents were all deleted is still kept.
		require.NoError(t, index.Delete(t.Context(), asha, []uuid.UUID{mine.ID}))
		kept, err := index.Owners(t.Context())
		require.NoError(t, err)
		require.NoError(t, index.DeleteOwner(t.Context(), asha))
		require.NoError(t, index.DeleteOwner(t.Context(), asha))
		left, err := index.Owners(t.Context())
		require.NoError(t, err)

		assert.ElementsMatch(t, []int64{asha, budi}, kept)
		assert.Equal(t, []int64{budi}, left)
	})

	t.Run("delete owner", func(t *testing.T) {
		index := open(t)
		mine := document(asha, ashaWork, "invoice")
		theirs := document(budi, budiAccount, "invoice")
		upsert(t, index, mine, theirs)

		require.NoError(t, index.DeleteOwner(t.Context(), asha))
		gone := find(t, index, asha, query("invoice"))
		kept := find(t, index, budi, query("invoice"))
		// Written again after the delete, the owner is back.
		upsert(t, index, mine)
		back := find(t, index, asha, query("invoice"))

		assert.Empty(t, gone.IDs)
		assert.Equal(t, []uuid.UUID{theirs.ID}, kept.IDs)
		assert.Equal(t, []uuid.UUID{mine.ID}, back.IDs)
	})

	t.Run("document IDs", func(t *testing.T) {
		index := open(t)
		first := document(asha, ashaWork, "invoice")
		second := document(asha, ashaHome, "receipt")
		upsert(t, index, first, second, document(budi, budiAccount, "invoice"))

		ids, err := index.DocumentIDs(t.Context(), asha)
		require.NoError(t, err)
		// A member nothing was ever written for.
		none, err := index.DocumentIDs(t.Context(), 3)
		require.NoError(t, err)

		assert.ElementsMatch(t, []uuid.UUID{first.ID, second.ID}, ids)
		assert.Empty(t, none)
	})

	t.Run("prepare twice", func(t *testing.T) {
		index := open(t)
		mine := document(asha, ashaWork, "invoice")
		upsert(t, index, mine)

		require.NoError(t, index.Prepare(t.Context()))
		result := find(t, index, asha, query("invoice"))

		assert.Equal(t, []uuid.UUID{mine.ID}, result.IDs)
	})
}

// document returns a message of owner's in account with subject, a new ID,
// and a thread of its own, received at a fixed time.
func document(owner, account int64, subject string) search.Document {
	id := uuid.New()
	return search.Document{
		ID:                 id,
		OwnerUserID:        owner,
		ConnectedAccountID: account,
		ThreadID:           "thread-" + id.String(),
		InternalDate:       time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC),
		Subject:            subject,
		From:               "Fajar <fajar@example.com>",
		FromAddresses:      []string{"fajar@example.com"},
	}
}

// query returns a search for text on the first page.
func query(text string) search.Query {
	q := firstPage
	q.Text = text
	return q
}

// upsert adds documents to index, failing the test on an error.
func upsert(t *testing.T, index search.Index, documents ...search.Document) {
	t.Helper()
	require.NoError(t, index.Upsert(t.Context(), documents))
}

// foundIDs is the IDs and total of a search's result.
type foundIDs struct {
	IDs   []uuid.UUID
	Total int
}

// find searches index as owner, failing the test on an error, and returns
// the IDs it found.
func find(t *testing.T, index search.Index, owner int64, q search.Query) foundIDs {
	t.Helper()
	result := hits(t, index, owner, q)
	out := foundIDs{IDs: []uuid.UUID{}, Total: result.Total}
	for _, hit := range result.Hits {
		out.IDs = append(out.IDs, hit.ID)
	}
	return out
}

// hits searches index as owner, failing the test on an error.
func hits(t *testing.T, index search.Index, owner int64, q search.Query) search.Result {
	t.Helper()
	result, err := index.Search(t.Context(), owner, q)
	require.NoError(t, err)
	return result
}
