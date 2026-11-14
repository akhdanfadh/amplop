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
// and mailbox filters, which fields a search matches, replacing, deleting within
// one owner's documents, paging, cancellation, and refused calls. open returns
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

		assert.Equal(t, search.Result{IDs: []uuid.UUID{mine.ID}, Total: 1}, plain)
		assert.Empty(t, named.IDs)
		assert.Equal(t, 0, named.Total)
		assert.Equal(t, search.Result{IDs: []uuid.UUID{mine.ID}, Total: 1}, mixed)
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

		assert.Equal(t, search.Result{IDs: []uuid.UUID{mine.ID}, Total: 1}, found)
		assert.Equal(t, search.Result{IDs: []uuid.UUID{mine.ID}, Total: 1}, kept)
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
		errs := map[string]error{
			"prepare":        index.Prepare(ctx),
			"upsert":         index.Upsert(ctx, []search.Document{added}),
			"delete":         index.Delete(ctx, asha, []uuid.UUID{added.ID}),
			"delete account": index.DeleteAccount(ctx, asha, ashaWork),
			"search":         searchErr,
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
		upsert(t, index, subject, from, to, cc, body)

		cases := map[string]uuid.UUID{"invoice": subject.ID, "citra": from.ID, "dewi": to.ID, "eka": cc.ID, "quarterly": body.ID}
		for word, want := range cases {
			result := find(t, index, asha, query(word))
			assert.Equal(t, []uuid.UUID{want}, result.IDs, word)
		}
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
		assert.Equal(t, search.Result{IDs: []uuid.UUID{after.ID}, Total: 1}, current)
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
		unowned := document(0, ashaWork, "invoice")
		noUpsertOwner := index.Upsert(t.Context(), []search.Document{document(asha, ashaWork, "invoice"), unowned})
		stored := find(t, index, asha, query("invoice"))

		require.ErrorIs(t, noOwner, search.ErrNoOwner)
		require.ErrorIs(t, noPage, search.ErrBadPage)
		require.ErrorIs(t, noSize, search.ErrBadPage)
		require.ErrorIs(t, noDeleteOwner, search.ErrNoOwner)
		require.ErrorIs(t, noAccountOwner, search.ErrNoOwner)
		// A batch with one unowned document writes none of it.
		require.ErrorIs(t, noUpsertOwner, search.ErrNoOwner)
		assert.Empty(t, stored.IDs)
	})

	t.Run("nothing to write", func(t *testing.T) {
		index := open(t)

		require.NoError(t, index.Upsert(t.Context(), nil))
		require.NoError(t, index.Delete(t.Context(), asha, nil))
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

// document returns a message of owner's in account with subject and a new
// ID, received at a fixed time.
func document(owner, account int64, subject string) search.Document {
	return search.Document{
		ID:                 uuid.New(),
		OwnerUserID:        owner,
		ConnectedAccountID: account,
		InternalDate:       time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC),
		Subject:            subject,
		From:               "Fajar <fajar@example.com>",
		FromAddress:        "fajar@example.com",
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

// find searches index as owner, failing the test on an error.
func find(t *testing.T, index search.Index, owner int64, q search.Query) search.Result {
	t.Helper()
	result, err := index.Search(t.Context(), owner, q)
	require.NoError(t, err)
	return result
}
