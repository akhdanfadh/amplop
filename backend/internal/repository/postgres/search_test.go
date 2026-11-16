package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
	"inbok/internal/search"
)

// searchWorkflows saves each search indexing job as rows of
// test_search_jobs, one per message ID, and each search account removal
// as a row of test_search_account_jobs, in the caller's transaction, or
// fails with err.
type searchWorkflows struct {
	Workflows
	err error
}

func (w searchWorkflows) EnqueueSearchIndexing(ctx context.Context, tx pgx.Tx, owner int64, ids []uuid.UUID) error {
	if w.err != nil {
		return w.err
	}
	_, err := tx.Exec(ctx, "INSERT INTO test_search_jobs (owner, message_id) SELECT $1, unnest($2::uuid[])", owner, ids)
	return err
}

func (w searchWorkflows) EnqueueSearchAccountRemoval(ctx context.Context, tx pgx.Tx, owner, accountID int64) error {
	if w.err != nil {
		return w.err
	}
	_, err := tx.Exec(ctx, "INSERT INTO test_search_account_jobs (owner, account_id) VALUES ($1, $2)", owner, accountID)
	return err
}

// createSearchJobTables creates the tables searchWorkflows saves jobs in.
func createSearchJobTables(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	_, err := conn.Exec(t.Context(), "CREATE TABLE test_search_jobs (owner bigint NOT NULL, message_id uuid NOT NULL)")
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), "CREATE TABLE test_search_account_jobs (owner bigint NOT NULL, account_id bigint NOT NULL)")
	require.NoError(t, err)
}

// searchJob is one message a saved search indexing job names.
type searchJob struct {
	Owner     int64
	MessageID uuid.UUID
}

// searchJobs returns the messages the saved search indexing jobs name, and
// empties the table, so a test sees only what its own change saved.
func searchJobs(t *testing.T, conn *pgx.Conn) []searchJob {
	t.Helper()
	rows, err := conn.Query(t.Context(), "DELETE FROM test_search_jobs RETURNING owner, message_id")
	require.NoError(t, err)
	jobs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[searchJob])
	require.NoError(t, err)
	return jobs
}

// jobsFor returns the jobs that index ids for owner.
func jobsFor(owner int64, ids ...uuid.UUID) []searchJob {
	jobs := make([]searchJob, len(ids))
	for i, id := range ids {
		jobs[i] = searchJob{Owner: owner, MessageID: id}
	}
	return jobs
}

// Every message a sync write saves, updates, or deletes, the earlier
// generations' sweep included, is named in one job for the account's
// member, saved with the write.
func TestSyncedMailSearchJobs(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{
		syncedMessage("saved"), syncedMessage("updated"), syncedMessage("deleted"), syncedMessage("earlier"),
	}})
	before := s.messages(t, s.account)
	saved := searchJobs(t, s.conn)

	s.update(t, s.account, mailsync.Change{
		State:   running(2),
		Saved:   []mailsync.Message{syncedMessage("saved")},
		Updated: []mailsync.MessageUpdate{{ProviderID: "updated", MailboxProviderIDs: []string{"TRASH"}, Version: 2}},
		Deleted: []string{"deleted"},
		// Generation 2 saved only "saved", so the sweep may take "updated"
		// and "earlier"; it takes one of them.
		DeleteEarlier: 1,
	})
	changed := searchJobs(t, s.conn)
	after := s.messages(t, s.account)
	// The one of "updated" and "earlier" the sweep took.
	swept := before["earlier"].ID
	if _, ok := after["updated"]; !ok {
		swept = before["updated"].ID
	}

	assert.ElementsMatch(t, jobsFor(s.member, before["saved"].ID, before["updated"].ID, before["deleted"].ID, before["earlier"].ID), saved)
	assert.Len(t, after, 2, "the sweep took one message")
	assert.ElementsMatch(t, jobsFor(s.member, before["saved"].ID, before["updated"].ID, before["deleted"].ID, swept), changed)
}

// A write to one message of a thread names every message of the thread,
// whose documents hold their conversation's places, and no other thread's:
// a reply to an archived message puts both in the inbox, and archiving or
// deleting the reply takes both out.
func TestSyncedMailSearchJobsThread(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	archived := syncedMessage("archived")
	archived.MailboxProviderIDs = nil
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{archived, syncedMessage("other")}})
	searchJobs(t, s.conn)
	reply := syncedMessage("reply")
	reply.ThreadID = archived.ThreadID

	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{reply}})
	saved := s.messages(t, s.account)
	replied := searchJobs(t, s.conn)
	s.update(t, s.account, mailsync.Change{State: running(1), Updated: []mailsync.MessageUpdate{
		{ProviderID: "reply", MailboxProviderIDs: []string{}, Version: 2},
	}})
	unlabelled := searchJobs(t, s.conn)
	s.update(t, s.account, mailsync.Change{State: running(1), Deleted: []string{"reply"}})
	deleted := searchJobs(t, s.conn)

	thread := jobsFor(s.member, saved["archived"].ID, saved["reply"].ID)
	assert.ElementsMatch(t, thread, replied)
	assert.ElementsMatch(t, thread, unlabelled)
	assert.ElementsMatch(t, thread, deleted)
}

// A mail action's labels, which [DB.UpdateMessages] writes, name the
// message they change in one job for the account's member, as a sync's
// do, so the index follows an archive before the next sync.
func TestUpdateMessagesSearchJob(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{
		syncedMessage("archived"), syncedMessage("kept"),
	}})
	saved := s.messages(t, s.account)
	searchJobs(t, s.conn)

	err := s.db.UpdateMessages(t.Context(), s.account, []mailsync.MessageUpdate{
		{ProviderID: "archived", MailboxProviderIDs: []string{"Label_1"}, Version: 2},
	})

	require.NoError(t, err)
	assert.Equal(t, jobsFor(s.member, saved["archived"].ID), searchJobs(t, s.conn))
}

// A write that changes no message saves no job.
func TestSyncedMailSearchNoJob(t *testing.T) {
	s := newSyncedMailTest(t)

	s.update(t, s.account, mailsync.Change{State: running(1), Mailboxes: gmailMailboxes})

	assert.Empty(t, searchJobs(t, s.conn))
}

// A search job that cannot be saved writes nothing of the change.
func TestSyncedMailSearchJobFails(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	refused := errors.New("queue refused")
	s.db = s.db.WithWorkflows(searchWorkflows{err: refused})

	_, err := s.db.UpdateSyncedMail(t.Context(), s.account, func(mailsync.SyncState) (mailsync.Change, error) {
		return mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1")}}, nil
	})

	require.ErrorIs(t, err, refused)
	assert.Empty(t, s.messages(t, s.account))
}

// Saving a message's parts saves its search job, and so does saving them
// again, as content stored again after the store lost it does; saving the
// content of a text part the provider sent apart saves one too, while an
// attachment's, or content a part already named, saves none.
func TestMessagePartsSearchJobs(t *testing.T) {
	s := newMessagePartsTest(t)
	ids := s.save(t, "m1")
	searchJobs(t, s.conn)
	tree := storedTree()
	// The HTML body arrives later, as Gmail sends a long one.
	html := []int{0, 1, 0}
	tree.Parts[0].Parts[1].Parts[0].BlobID = uuid.Nil()

	_, err := s.db.SaveMessageParts(t.Context(), s.account, ids[0], tree)
	require.NoError(t, err)
	parts := searchJobs(t, s.conn)
	kept, err := s.db.SaveMessageParts(t.Context(), s.account, ids[0], tree)
	require.NoError(t, err)
	restored := searchJobs(t, s.conn)
	blob := uuid.New()
	_, err = s.db.SavePartBlob(t.Context(), s.account, ids[0], html, blob)
	require.NoError(t, err)
	body := searchJobs(t, s.conn)
	_, err = s.db.SavePartBlob(t.Context(), s.account, ids[0], html, uuid.New())
	require.NoError(t, err)
	again := searchJobs(t, s.conn)
	_, err = s.db.SavePartBlob(t.Context(), s.account, ids[0], []int{1}, uuid.New())
	require.NoError(t, err)
	attachment := searchJobs(t, s.conn)

	assert.Equal(t, jobsFor(s.member, ids[0]), parts)
	assert.False(t, kept, "the parts saved before are kept")
	assert.Equal(t, jobsFor(s.member, ids[0]), restored)
	assert.Equal(t, jobsFor(s.member, ids[0]), body)
	assert.Empty(t, again, "the part named its blob already")
	assert.Empty(t, attachment)
}

// Disconnecting an account saves the job that removes it from its
// member's search index, with the disconnect.
func TestConnectedAccountSearchRemoval(t *testing.T) {
	c := newConnectedAccountsTest(t)
	account, _ := c.connectAlex(t)

	_, err := c.service.Disconnect(t.Context(), c.member, account.ID)
	require.NoError(t, err)
	var owner, accountID int64
	require.NoError(t, c.conn.QueryRow(t.Context(), "SELECT owner, account_id FROM test_search_account_jobs").Scan(&owner, &accountID))

	assert.Equal(t, []int64{c.member, account.ID}, []int64{owner, accountID})
}

// A disconnect whose search job cannot be saved keeps the account.
func TestConnectedAccountSearchRemovalFails(t *testing.T) {
	c := newConnectedAccountsTest(t)
	account, _ := c.connectAlex(t)
	failed := errors.New("queue refused")
	workflows := accountWorkflows{searchWorkflows: searchWorkflows{err: failed}}

	_, err := connectedaccount.NewService(c.db.WithWorkflows(workflows), c.ring).Disconnect(t.Context(), c.member, account.ID)
	accounts, listErr := c.db.ListConnectedAccounts(t.Context(), c.member)

	require.ErrorIs(t, err, failed)
	require.NoError(t, listErr)
	assert.Len(t, accounts, 1)
}

// A member's index sources are their messages among the IDs, with
// addresses as stored, mailboxes by inbok's IDs in order, whether parts
// are stored, and their conversation's mailboxes and star, none for a
// draft alone in its thread; another member's message, and an unknown ID,
// are left out.
func TestIndexSources(t *testing.T) {
	s := newMessagePartsTest(t)
	ids := s.save(t, "m1", "m2")
	_, err := s.db.SaveMessageParts(t.Context(), s.account, ids[1], storedTree())
	require.NoError(t, err)
	// Archived and not starred itself, in m1's conversation.
	archived := syncedMessage("archived")
	archived.ThreadID, archived.MailboxProviderIDs, archived.Flagged = "thread-m1", nil, false
	draft := syncedMessage("draft")
	draft.MailboxProviderIDs = []string{"DRAFT"}
	mailboxes := append(slices.Clone(gmailMailboxes), mailsync.Mailbox{ProviderID: "DRAFT", Name: "DRAFT", Role: mailsync.RoleDrafts, MadeBy: mailsync.MadeByProvider})
	s.update(t, s.account, mailsync.Change{State: running(1), Mailboxes: mailboxes, Saved: []mailsync.Message{archived, draft}})
	saved := s.messages(t, s.account)
	stranger := insertConnectingMember(t, s.conn, "stranger@example.com")
	theirAccount := insertSyncingAccount(t, s.conn, stranger, "sub-stranger")
	s.update(t, theirAccount, mailsync.Change{State: running(1), Mailboxes: gmailMailboxes, Saved: []mailsync.Message{syncedMessage("theirs")}})
	theirs := s.messages(t, theirAccount)["theirs"].ID

	sources, err := s.db.IndexSources(t.Context(), s.member, []uuid.UUID{ids[0], ids[1], saved["archived"].ID, saved["draft"].ID, theirs, uuid.New()})
	require.NoError(t, err)

	inboxAndReceipts := []uuid.UUID{s.mailboxID(t, s.account, "INBOX"), s.mailboxID(t, s.account, "Label_1")}
	slices.SortFunc(inboxAndReceipts, uuid.UUID.Compare)
	m := syncedMessage("m1")
	want := search.Source{
		ID:                     ids[0],
		AccountID:              s.account,
		ThreadID:               "thread-m1",
		InternalDate:           time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC),
		Subject:                m.Subject,
		From:                   m.From,
		To:                     m.To,
		CC:                     m.CC,
		MailboxIDs:             inboxAndReceipts,
		ConversationMailboxIDs: inboxAndReceipts,
		ConversationFlagged:    true,
	}
	withParts := want
	withParts.ID = ids[1]
	withParts.ThreadID = "thread-m2"
	withParts.HasParts = true
	inThread := want
	inThread.ID = saved["archived"].ID
	inThread.MailboxIDs = []uuid.UUID{}
	alone := want
	alone.ID = saved["draft"].ID
	alone.ThreadID = "thread-draft"
	alone.MailboxIDs = []uuid.UUID{s.mailboxID(t, s.account, "DRAFT")}
	alone.ConversationMailboxIDs = []uuid.UUID{}
	alone.ConversationFlagged = false
	assert.ElementsMatch(t, []search.Source{want, withParts, inThread, alone}, sources)
}

// A rebuild reads every member, mail or not, and each member's messages
// in every account, never another member's.
func TestRebuildReads(t *testing.T) {
	s := newMessagePartsTest(t)
	ids := s.save(t, "m1", "m2")
	second := insertSyncingAccount(t, s.conn, s.member, "sub-second")
	s.update(t, second, mailsync.Change{State: running(1), Mailboxes: gmailMailboxes, Saved: []mailsync.Message{syncedMessage("m3")}})
	ids = append(ids, s.messages(t, second)["m3"].ID)
	stranger := insertConnectingMember(t, s.conn, "stranger@example.com")
	theirAccount := insertSyncingAccount(t, s.conn, stranger, "sub-stranger")
	s.update(t, theirAccount, mailsync.Change{State: running(1), Mailboxes: gmailMailboxes, Saved: []mailsync.Message{syncedMessage("theirs")}})
	theirs := s.messages(t, theirAccount)["theirs"].ID
	lonely := insertConnectingMember(t, s.conn, "lonely@example.com")

	users, err := s.db.UserIDs(t.Context())
	require.NoError(t, err)
	exists, err := s.db.UserExists(t.Context(), lonely)
	require.NoError(t, err)
	missing, err := s.db.UserExists(t.Context(), lonely+1)
	require.NoError(t, err)
	mine, err := s.db.MessageIDs(t.Context(), s.member)
	require.NoError(t, err)
	theirIDs, err := s.db.MessageIDs(t.Context(), stranger)
	require.NoError(t, err)
	none, err := s.db.MessageIDs(t.Context(), lonely)
	require.NoError(t, err)

	assert.Subset(t, users, []int64{s.member, stranger, lonely})
	assert.True(t, exists)
	assert.False(t, missing)
	assert.ElementsMatch(t, ids, mine)
	assert.Equal(t, []uuid.UUID{theirs}, theirIDs)
	assert.Empty(t, none)
}

// A search reads whether an account is the member's, the IDs of the
// member's mailboxes with given roles in every account, and the member's
// found messages as a result shows them, leaving out those in such a
// mailbox now, another member's, and unknown IDs.
func TestSearchReads(t *testing.T) {
	s := newMessagePartsTest(t)
	mailboxes := append(slices.Clone(gmailMailboxes), mailsync.Mailbox{ProviderID: "DRAFT", Name: "DRAFT", Role: mailsync.RoleDrafts, MadeBy: mailsync.MadeByProvider})
	shown, spam, trash, draft := syncedMessage("shown"), syncedMessage("spam"), syncedMessage("trash"), syncedMessage("draft")
	shown.Seen = false
	spam.MailboxProviderIDs = []string{"INBOX", "SPAM"}
	trash.MailboxProviderIDs = []string{"TRASH"}
	draft.MailboxProviderIDs = []string{"DRAFT"}
	s.update(t, s.account, mailsync.Change{State: running(1), Mailboxes: mailboxes, Saved: []mailsync.Message{shown, spam, trash, draft}})
	saved := s.messages(t, s.account)
	stranger := insertConnectingMember(t, s.conn, "stranger@example.com")
	theirAccount := insertSyncingAccount(t, s.conn, stranger, "sub-stranger")
	s.update(t, theirAccount, mailsync.Change{State: running(1), Mailboxes: gmailMailboxes, Saved: []mailsync.Message{syncedMessage("theirs")}})
	theirs := s.messages(t, theirAccount)["theirs"].ID
	hidden := []mailsync.Role{mailsync.RoleJunk, mailsync.RoleTrash, mailsync.RoleDrafts}

	owns, err := s.db.OwnsAccount(t.Context(), s.member, s.account)
	require.NoError(t, err)
	foreign, err := s.db.OwnsAccount(t.Context(), s.member, theirAccount)
	require.NoError(t, err)
	missing, err := s.db.OwnsAccount(t.Context(), s.member, theirAccount+1000)
	require.NoError(t, err)
	boxes, err := s.db.MailboxIDs(t.Context(), s.member, hidden)
	require.NoError(t, err)
	found, err := s.db.FoundMessages(t.Context(), s.member, []uuid.UUID{
		saved["shown"].ID, saved["spam"].ID, saved["trash"].ID, saved["draft"].ID, theirs, uuid.New(),
	}, hidden)
	require.NoError(t, err)

	assert.True(t, owns)
	assert.False(t, foreign)
	assert.False(t, missing)
	// The member's other account's too; never the stranger's.
	assert.ElementsMatch(t, []uuid.UUID{
		s.mailboxID(t, s.account, "SPAM"), s.mailboxID(t, s.account, "TRASH"), s.mailboxID(t, s.account, "DRAFT"),
		s.mailboxID(t, s.other, "SPAM"), s.mailboxID(t, s.other, "TRASH"),
	}, boxes)
	assert.Equal(t, []search.Message{{
		ID:        saved["shown"].ID,
		AccountID: s.account,
		Date:      shown.InternalDate,
		From:      shown.From,
		Subject:   shown.Subject,
		Preview:   shown.Preview,
		Unread:    true,
		Starred:   true,
	}}, found)
}
