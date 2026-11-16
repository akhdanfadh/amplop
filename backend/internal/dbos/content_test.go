package dbos

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/encryption"
	"inbok/internal/mailsync"
	"inbok/internal/messagecontent"
	"inbok/internal/repository/postgres"
)

// fakeContent records what it is asked to delete, and answers err.
type fakeContent struct {
	err error

	mu       sync.Mutex
	deleted  []messagecontent.Ref
	accounts []int64
	calls    int
}

func (f *fakeContent) Delete(_ context.Context, refs []messagecontent.Ref) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err == nil {
		f.deleted = append(f.deleted, refs...)
	}
	return f.err
}

func (f *fakeContent) DeleteAccount(_ context.Context, account int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err == nil {
		f.accounts = append(f.accounts, account)
	}
	return f.err
}

// state returns the refs and accounts deleted, and how many calls came.
func (f *fakeContent) state() ([]messagecontent.Ref, []int64, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.deleted), slices.Clone(f.accounts), f.calls
}

// contentWorker launches a worker on url deleting content from store,
// without Google settings.
func contentWorker(t *testing.T, url string, store contentStore) *Worker {
	t.Helper()
	worker, err := NewWorker(url, "worker", Services{MessageContent: store}, discard)
	require.NoError(t, err)
	require.NoError(t, worker.Launch())
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })
	return worker
}

func TestContentDeletion(t *testing.T) {
	store := &fakeContent{}
	worker := contentWorker(t, migrated(t), store)
	ids := []uuid.UUID{uuid.New(), uuid.New()}

	handle, err := dbos.Enqueue[int](worker.ctx, contentQueue, contentDeletionWorkflow, ContentDeletionInput{AccountID: 7, BlobIDs: ids})
	require.NoError(t, err)
	deleted, err := handle.GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, 2, deleted)
	refs, _, _ := store.state()
	assert.Equal(t, []messagecontent.Ref{{Account: 7, ID: ids[0]}, {Account: 7, ID: ids[1]}}, refs)
}

// A store that keeps failing is retried with its policy, and the run
// fails, leaving the content for the sweep.
func TestContentDeletionGivesUp(t *testing.T) {
	quickRetries(t, &contentRetries)
	store := &fakeContent{err: errors.New("store down")}
	worker := contentWorker(t, migrated(t), store)

	handle, err := dbos.Enqueue[int](worker.ctx, contentQueue, contentDeletionWorkflow, ContentDeletionInput{AccountID: 7, BlobIDs: []uuid.UUID{uuid.New()}})
	require.NoError(t, err)
	_, err = handle.GetResult(dbos.WithHandleTimeout(wait))

	require.ErrorContains(t, err, "store down")
	_, _, calls := store.state()
	assert.Equal(t, 6, calls, "the first attempt and 5 retries")
}

// Content deletions are retried 5 more times, waiting 1, 2, 4, 8, then 15
// minutes, as the docs promise.
func TestContentRetryPolicy(t *testing.T) {
	assert.Equal(t, retries{attempts: 5, base: time.Minute, ceiling: 15 * time.Minute}, contentRetries)
}

// A disconnected account's content is deleted by prefix; a store that
// keeps failing is retried with its policy, and the run fails, leaving
// the content for the sweep.
func TestAccountContentDeletion(t *testing.T) {
	quickRetries(t, &contentRetries)
	for name, storeErr := range map[string]error{"deleted": nil, "store down": errors.New("store down")} {
		t.Run(name, func(t *testing.T) {
			store := &fakeContent{err: storeErr}
			worker := contentWorker(t, migrated(t), store)

			handle, err := dbos.Enqueue[struct{}](worker.ctx, contentQueue, accountContentDeletionWorkflow, AccountContentDeletionInput{AccountID: 9})
			require.NoError(t, err)
			_, err = handle.GetResult(dbos.WithHandleTimeout(wait))

			_, accounts, calls := store.state()
			if storeErr != nil {
				require.ErrorContains(t, err, "store down")
				assert.Equal(t, 6, calls, "the first attempt and 5 retries")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []int64{9}, accounts)
		})
	}
}

// A disconnect saves the deletion of the account's content beside its
// removal, which a worker without Google settings runs.
func TestClientAccountContent(t *testing.T) {
	s := newContentStack(t)

	var member int64
	require.NoError(t, s.conn.QueryRow(t.Context(), "SELECT user_id FROM connected_accounts WHERE id = $1", s.account).Scan(&member))
	_, err := s.db.DeleteConnectedAccount(t.Context(), member, s.account)
	require.NoError(t, err)

	store := &fakeContent{}
	contentWorker(t, s.url, store)
	require.Eventually(t, func() bool { _, accounts, _ := store.state(); return len(accounts) == 1 }, wait, 50*time.Millisecond)
	_, accounts, _ := store.state()
	assert.Equal(t, []int64{s.account}, accounts)
}

// contentStack is a [clientStack] with a connected account holding one
// synchronized message whose parts name stored blobs.
type contentStack struct {
	db      *postgres.DB
	conn    *pgx.Conn
	url     string
	update  func(context.Context, mailsync.Change) error
	account int64
	blobs   []uuid.UUID
}

func newContentStack(t *testing.T) contentStack {
	t.Helper()
	db, conn, url := clientStack(t)
	var member int64
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT id FROM users WHERE email = 'admin@example.com'").Scan(&member))
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	account, err := connectedaccount.NewService(db, ring).Connect(t.Context(), member,
		connectedaccount.Identity{Subject: "100000000000000000001", Email: "alex@example.com"}, "refresh-secret-1")
	require.NoError(t, err)

	update := func(ctx context.Context, change mailsync.Change) error {
		_, err := db.UpdateSyncedMail(ctx, account.ID, func(mailsync.SyncState) (mailsync.Change, error) {
			return change, nil
		})
		return err
	}
	running := mailsync.SyncState{Status: mailsync.StatusRunning, Position: "1", Generation: 1}
	require.NoError(t, update(t.Context(), mailsync.Change{State: running, Mailboxes: []mailsync.Mailbox{}, Saved: []mailsync.Message{{
		ProviderID: "m1", ThreadID: "t1", InternalDate: time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC), Version: 1,
	}}}))
	var message uuid.UUID
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT id FROM messages WHERE connected_account_id = $1", account.ID).Scan(&message))
	blobs := []uuid.UUID{uuid.New(), uuid.New()}
	_, err = db.SaveMessageParts(t.Context(), account.ID, message, messagecontent.Part{
		MediaType: "multipart/alternative", Kind: messagecontent.KindMultipart, Parts: []messagecontent.Part{
			{ID: "0", MediaType: "text/plain", Kind: messagecontent.KindText, BlobID: blobs[0]},
			{ID: "1", MediaType: "text/html", Kind: messagecontent.KindText, BlobID: blobs[1]},
		},
	})
	require.NoError(t, err)
	return contentStack{db: db, conn: conn, url: url, update: update, account: account.ID, blobs: blobs}
}

// contentDeletions returns the statuses of the content deletion runs.
func contentDeletions(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	rows, err := conn.Query(t.Context(), "SELECT status FROM dbos.workflow_status WHERE name = $1", contentDeletionWorkflow)
	require.NoError(t, err)
	statuses, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return statuses
}

// Synchronization deleting a message saves the deletion of its content,
// which a worker runs.
func TestClientContentDeletion(t *testing.T) {
	s := newContentStack(t)

	require.NoError(t, s.update(t.Context(), mailsync.Change{
		State:   mailsync.SyncState{Status: mailsync.StatusRunning, Position: "1", Generation: 1},
		Deleted: []string{"m1"},
	}))

	assert.Equal(t, []string{"ENQUEUED"}, contentDeletions(t, s.conn))
	store := &fakeContent{}
	contentWorker(t, s.url, store)
	require.Eventually(t, func() bool { refs, _, _ := store.state(); return len(refs) == 2 }, wait, 50*time.Millisecond)
	refs, _, _ := store.state()
	assert.ElementsMatch(t, []messagecontent.Ref{{Account: s.account, ID: s.blobs[0]}, {Account: s.account, ID: s.blobs[1]}}, refs)
}

// A delete that fails as it commits rolls its content deletion back with
// it, and keeps the message.
func TestClientContentRollback(t *testing.T) {
	s := newContentStack(t)
	indexing := runs(t, s.conn, searchIndexingWorkflow)
	_, err := s.conn.Exec(t.Context(), `CREATE FUNCTION reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'delete rejected'; END $$`)
	require.NoError(t, err)
	_, err = s.conn.Exec(t.Context(), "CREATE CONSTRAINT TRIGGER reject_delete AFTER DELETE ON messages DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_delete()")
	require.NoError(t, err)

	err = s.update(t.Context(), mailsync.Change{
		State:   mailsync.SyncState{Status: mailsync.StatusRunning, Position: "1", Generation: 1},
		Deleted: []string{"m1"},
	})

	require.ErrorContains(t, err, "delete rejected")
	assert.Empty(t, contentDeletions(t, s.conn))
	assert.Equal(t, indexing, runs(t, s.conn, searchIndexingWorkflow), "no search run for the delete")
	var messages int
	require.NoError(t, s.conn.QueryRow(t.Context(), "SELECT count(*) FROM messages").Scan(&messages))
	assert.Equal(t, 1, messages)
}
