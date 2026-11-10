package postgres

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/mailsync"
)

// syncedMailTest is a migrated database with one member who has two
// connected accounts, each with a sync state that is waiting.
type syncedMailTest struct {
	db      *DB
	conn    *pgx.Conn
	account int64
	other   int64
}

func newSyncedMailTest(t *testing.T) syncedMailTest {
	t.Helper()
	db, conn := setupStorage(t)
	member := insertConnectingMember(t, conn, "member@example.com")
	return syncedMailTest{
		db:      db,
		conn:    conn,
		account: insertSyncingAccount(t, conn, member, "sub-alex"),
		other:   insertSyncingAccount(t, conn, member, "sub-blair"),
	}
}

// insertSyncingAccount adds userID's Gmail account with subject and its
// waiting sync state, and returns the account's ID.
func insertSyncingAccount(t *testing.T, conn *pgx.Conn, userID int64, subject string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, conn.QueryRow(t.Context(), `
		INSERT INTO connected_accounts (user_id, provider, email, oauth_subject, refresh_token_key_id, refresh_token_ciphertext)
		VALUES ($1, 'gmail', $2 || '@example.com', $2, '1', '\x01') RETURNING id`, userID, subject).Scan(&id))
	_, err := conn.Exec(t.Context(), "INSERT INTO sync_states (connected_account_id, status) VALUES ($1, 'waiting')", id)
	require.NoError(t, err)
	return id
}

// gmailMailboxes are mailboxes as a Gmail account lists them, ordered by
// provider ID.
var gmailMailboxes = []mailsync.Mailbox{
	{ProviderID: "CATEGORY_SOCIAL", Name: "CATEGORY_SOCIAL", Role: mailsync.RoleNone},
	{ProviderID: "INBOX", Name: "INBOX", Role: mailsync.RoleInbox},
	{ProviderID: "Label_1", Name: "Receipts", Role: mailsync.RoleNone},
	{ProviderID: "SPAM", Name: "SPAM", Role: mailsync.RoleJunk},
	{ProviderID: "TRASH", Name: "TRASH", Role: mailsync.RoleTrash},
}

// syncedMessage returns a message with every field set, under providerID,
// in the inbox and Receipts.
func syncedMessage(providerID string) mailsync.Message {
	return mailsync.Message{
		ProviderID:         providerID,
		ThreadID:           "thread-" + providerID,
		InternalDate:       time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC),
		Subject:            "Quarterly report",
		From:               []mailsync.Address{{Name: "Asha K", Address: "asha@example.co.id"}},
		ReplyTo:            []mailsync.Address{{Address: "reports@example.co.id"}},
		To:                 []mailsync.Address{{Name: "Member", Address: "member@example.com"}, {Address: "team@example.com"}},
		CC:                 []mailsync.Address{{Name: "undisclosed-recipients:;"}},
		Preview:            "Numbers attached",
		Seen:               true,
		Flagged:            true,
		MailboxProviderIDs: []string{"INBOX", "Label_1"},
		MessageID:          "<report-" + providerID + "@example.co.id>",
		InReplyTo:          []string{"<request@example.com>"},
		References:         []string{"<thread@example.com>", "<request@example.com>"},
	}
}

// running is the state of a full sync of generation that has started.
func running(generation int) mailsync.SyncState {
	return mailsync.SyncState{Status: mailsync.StatusRunning, Position: "4096", Generation: generation}
}

// update writes change to accountID's synced mail, failing the test on an
// error, and returns the state it saved.
func (s syncedMailTest) update(t *testing.T, accountID int64, change mailsync.Change) mailsync.SyncState {
	t.Helper()
	return s.write(t, accountID, change).State
}

// write writes change to accountID's synced mail, failing the test on an
// error, and returns what it wrote.
func (s syncedMailTest) write(t *testing.T, accountID int64, change mailsync.Change) mailsync.Written {
	t.Helper()
	written, err := s.db.UpdateSyncedMail(t.Context(), accountID, func(mailsync.SyncState) (mailsync.Change, error) {
		return change, nil
	})
	require.NoError(t, err)
	return written
}

// withMailboxes gives both accounts [gmailMailboxes].
func (s syncedMailTest) withMailboxes(t *testing.T) syncedMailTest {
	t.Helper()
	s.update(t, s.account, mailsync.Change{State: running(1), Mailboxes: gmailMailboxes})
	s.update(t, s.other, mailsync.Change{State: running(1), Mailboxes: gmailMailboxes})
	return s
}

// messages returns accountID's messages by provider ID, as saved, each
// with its mailboxes' provider IDs in order.
func (s syncedMailTest) messages(t *testing.T, accountID int64) map[string]mailsync.Message {
	t.Helper()
	rows, err := s.conn.Query(t.Context(), `
		SELECT m.id, m.connected_account_id, m.provider_message_id, m.provider_thread_id, m.internal_date, m.subject,
			m.from_addresses, m.reply_to_addresses, m.to_addresses, m.cc_addresses, m.preview, m.seen, m.flagged,
			m.internet_message_id, m.in_reply_to, m.internet_references,
			array(SELECT mb.provider_mailbox_id FROM message_mailboxes mm JOIN mailboxes mb ON mb.id = mm.mailbox_id
				WHERE mm.message_id = m.id ORDER BY mb.provider_mailbox_id)
		FROM messages m WHERE m.connected_account_id = $1`, accountID)
	require.NoError(t, err)
	defer rows.Close()

	messages := map[string]mailsync.Message{}
	for rows.Next() {
		var m mailsync.Message
		var from, replyTo, to, cc []byte
		require.NoError(t, rows.Scan(&m.ID, &m.AccountID, &m.ProviderID, &m.ThreadID, &m.InternalDate, &m.Subject,
			&from, &replyTo, &to, &cc, &m.Preview, &m.Seen, &m.Flagged, &m.MessageID, &m.InReplyTo, &m.References,
			&m.MailboxProviderIDs))
		m.InternalDate = m.InternalDate.UTC()
		for _, field := range []struct {
			raw  []byte
			into *[]mailsync.Address
		}{{from, &m.From}, {replyTo, &m.ReplyTo}, {to, &m.To}, {cc, &m.CC}} {
			var addresses []struct{ Name, Address string }
			require.NoError(t, json.Unmarshal(field.raw, &addresses))
			for _, a := range addresses {
				*field.into = append(*field.into, mailsync.Address{Name: a.Name, Address: a.Address})
			}
		}
		messages[m.ProviderID] = m
	}
	require.NoError(t, rows.Err())
	return messages
}

// mailboxes returns accountID's mailboxes, ordered by provider ID.
func (s syncedMailTest) mailboxes(t *testing.T, accountID int64) []mailsync.Mailbox {
	t.Helper()
	rows, err := s.conn.Query(t.Context(), `
		SELECT provider_mailbox_id, name, coalesce(role, '') FROM mailboxes
		WHERE connected_account_id = $1 ORDER BY provider_mailbox_id`, accountID)
	require.NoError(t, err)
	mailboxes, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (mailsync.Mailbox, error) {
		var m mailsync.Mailbox
		err := row.Scan(&m.ProviderID, &m.Name, &m.Role)
		return m, err
	})
	require.NoError(t, err)
	return mailboxes
}

// mailboxID returns the ID of accountID's mailbox providerID.
func (s syncedMailTest) mailboxID(t *testing.T, accountID int64, providerID string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, s.conn.QueryRow(t.Context(), "SELECT id FROM mailboxes WHERE connected_account_id = $1 AND provider_mailbox_id = $2", accountID, providerID).Scan(&id))
	return id
}

// generation returns accountID's saved sync generation, read directly.
func (s syncedMailTest) generation(t *testing.T, accountID int64) int {
	t.Helper()
	var generation int
	require.NoError(t, s.conn.QueryRow(t.Context(), "SELECT sync_generation FROM sync_states WHERE connected_account_id = $1", accountID).Scan(&generation))
	return generation
}

func TestSyncedMailSaveMessages(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	sent := syncedMessage("m1")

	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{sent, syncedMessage("m2")}})

	saved := s.messages(t, s.account)
	require.Len(t, saved, 2)
	got := saved["m1"]
	assert.NotEqual(t, uuid.Nil(), got.ID)
	assert.NotEqual(t, got.ID, saved["m2"].ID)
	sent.ID = got.ID
	sent.AccountID = s.account
	assert.Equal(t, sent, got)
	assert.Empty(t, s.messages(t, s.other))
}

func TestSyncedMailSaveEmptyLists(t *testing.T) {
	s := newSyncedMailTest(t)
	unlabelled := mailsync.Message{ProviderID: "m1", ThreadID: "t1", InternalDate: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}

	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{unlabelled}})

	got := s.messages(t, s.account)["m1"]
	assert.Empty(t, got.From)
	assert.Empty(t, got.MailboxProviderIDs, "a message with no label is in no mailbox")
	assert.Empty(t, got.References)
	assert.Empty(t, got.MessageID)
	assert.False(t, got.Seen)
}

func TestSyncedMailSaveAgain(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1")}})
	id := s.messages(t, s.account)["m1"].ID

	again := syncedMessage("m1")
	again.MailboxProviderIDs = []string{"TRASH", "TRASH"}
	again.Seen = false
	again.Subject = "Re: Quarterly report"
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{again}})

	saved := s.messages(t, s.account)
	require.Len(t, saved, 1)
	assert.Equal(t, id, saved["m1"].ID, "a saved message keeps its ID")
	assert.Equal(t, []string{"TRASH"}, saved["m1"].MailboxProviderIDs, "its mailboxes are replaced")
	assert.False(t, saved["m1"].Seen)
	assert.Equal(t, "Re: Quarterly report", saved["m1"].Subject)
}

func TestSyncedMailTwoAccounts(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)

	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1")}})
	s.update(t, s.other, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1")}})

	assert.NotEqual(t, s.messages(t, s.account)["m1"].ID, s.messages(t, s.other)["m1"].ID)
	var otherAccountMailboxes int
	require.NoError(t, s.conn.QueryRow(t.Context(), `
		SELECT count(*) FROM message_mailboxes mm
		JOIN messages m ON m.id = mm.message_id JOIN mailboxes mb ON mb.id = mm.mailbox_id
		WHERE m.connected_account_id <> mb.connected_account_id`).Scan(&otherAccountMailboxes))
	assert.Equal(t, 0, otherAccountMailboxes, "a message is only ever in its own account's mailboxes")
}

func TestSyncedMailCrossAccount(t *testing.T) {
	// Given another member's account with an inbox, and a message of this
	// account's.
	s := newSyncedMailTest(t).withMailboxes(t)
	stranger := insertSyncingAccount(t, s.conn, insertConnectingMember(t, s.conn, "stranger@example.com"), "sub-casey")
	s.update(t, stranger, mailsync.Change{State: running(1), Mailboxes: gmailMailboxes})
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1")}})
	message := s.messages(t, s.account)["m1"].ID
	inbox := s.mailboxID(t, stranger, "INBOX")

	// When the message is put in the stranger's inbox, under either account.
	_, underStranger := s.conn.Exec(t.Context(), "INSERT INTO message_mailboxes (connected_account_id, message_id, mailbox_id) VALUES ($1, $2, $3)", stranger, message, inbox)
	_, underOwner := s.conn.Exec(t.Context(), "INSERT INTO message_mailboxes (connected_account_id, message_id, mailbox_id) VALUES ($1, $2, $3)", s.account, message, inbox)

	// Then the database refuses both.
	require.Error(t, underStranger)
	require.Error(t, underOwner)
}

func TestSyncedMailUnknownMailbox(t *testing.T) {
	// Given a saved message, and a Label_9 that only the other account has.
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m0")}})
	s.update(t, s.other, mailsync.Change{State: running(1), Mailboxes: append(slices.Clone(gmailMailboxes), mailsync.Mailbox{ProviderID: "Label_9", Name: "New"})})
	before := s.messages(t, s.account)
	unknown := syncedMessage("m1")
	unknown.MailboxProviderIDs = []string{"INBOX", "Label_9"}

	// When a change that also deletes m0 and starts generation 2 saves a
	// message in Label_9.
	_, err := s.db.UpdateSyncedMail(t.Context(), s.account, func(mailsync.SyncState) (mailsync.Change, error) {
		return mailsync.Change{State: running(2), Deleted: []string{"m0"}, Saved: []mailsync.Message{unknown}}, nil
	})

	// Then it is refused and none of it is written.
	require.ErrorIs(t, err, mailsync.ErrUnknownMailbox)
	assert.Equal(t, before, s.messages(t, s.account))
	assert.Equal(t, 1, s.generation(t, s.account))
}

func TestSyncedMailNewMailbox(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	message := syncedMessage("m1")
	message.MailboxProviderIDs = []string{"Label_9"}

	s.update(t, s.account, mailsync.Change{
		State:     running(1),
		Mailboxes: append(slices.Clone(gmailMailboxes), mailsync.Mailbox{ProviderID: "Label_9", Name: "New"}),
		Saved:     []mailsync.Message{message},
	})

	assert.Equal(t, []string{"Label_9"}, s.messages(t, s.account)["m1"].MailboxProviderIDs)
}

func TestSyncedMailDuplicateSaves(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	inInbox := syncedMessage("m1")
	inInbox.MailboxProviderIDs = []string{"INBOX"}
	inTrash := syncedMessage("m1")
	inTrash.MailboxProviderIDs = []string{"TRASH"}
	inTrash.Subject = "Later"

	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{inInbox, inTrash}})
	s.update(t, s.other, mailsync.Change{State: running(1), Saved: []mailsync.Message{inInbox, inInbox}})

	saved := s.messages(t, s.account)
	require.Len(t, saved, 1)
	assert.Equal(t, "Later", saved["m1"].Subject, "the last save wins")
	assert.Equal(t, []string{"TRASH"}, saved["m1"].MailboxProviderIDs)
	assert.Equal(t, []string{"INBOX"}, s.messages(t, s.other)["m1"].MailboxProviderIDs, "one save given twice is saved once")
}

func TestSyncedMailDelete(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	both := []mailsync.Message{syncedMessage("m1"), syncedMessage("m2")}
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: both})
	s.update(t, s.other, mailsync.Change{State: running(1), Saved: both})

	s.update(t, s.account, mailsync.Change{State: running(1), Deleted: []string{"m1", "unknown"}})

	assert.Equal(t, []string{"m2"}, keys(s.messages(t, s.account)))
	assert.Len(t, s.messages(t, s.other), 2, "another account's message with the same ID stays")
	var memberships int
	require.NoError(t, s.conn.QueryRow(t.Context(), "SELECT count(*) FROM message_mailboxes").Scan(&memberships))
	assert.Equal(t, 6, memberships, "the deleted message's memberships go with it")
}

func TestSyncedMailDeleteThenSave(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1")}})
	id := s.messages(t, s.account)["m1"].ID
	change := mailsync.Change{State: running(1), Deleted: []string{"m1"}, Saved: []mailsync.Message{syncedMessage("m1")}}

	s.update(t, s.account, change)
	s.update(t, s.account, change)

	saved := s.messages(t, s.account)
	assert.Equal(t, []string{"m1"}, keys(saved), "a save wins over a delete")
	assert.Equal(t, id, saved["m1"].ID, "and keeps the message's ID however often it repeats")
}

func TestSyncedMailDeleteEarlier(t *testing.T) {
	// Given generation 1 saved three messages, and another account's from
	// the same generation number.
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{
		syncedMessage("gone-1"), syncedMessage("gone-2"), syncedMessage("kept"),
	}})
	s.update(t, s.other, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("theirs")}})
	kept := s.messages(t, s.account)["kept"].ID

	// When generation 2 saves one of them in the same change that deletes
	// one earlier message, then deletes up to five more, then more again.
	first := s.write(t, s.account, mailsync.Change{State: running(2), Saved: []mailsync.Message{syncedMessage("kept")}, DeleteEarlier: 1})
	afterFirst := len(s.messages(t, s.account))
	second := s.write(t, s.account, mailsync.Change{State: running(2), DeleteEarlier: 5})
	third := s.write(t, s.account, mailsync.Change{State: running(2), DeleteEarlier: 5})

	// Then each change deletes at most its number of the ones generation 2
	// did not save, and says how many; the saved one keeps its ID.
	assert.Equal(t, []int{1, 1, 0}, []int{first.EarlierDeleted, second.EarlierDeleted, third.EarlierDeleted})
	assert.Equal(t, 2, afterFirst)
	saved := s.messages(t, s.account)
	assert.Equal(t, []string{"kept"}, keys(saved))
	assert.Equal(t, kept, saved["kept"].ID)
	assert.Equal(t, []string{"theirs"}, keys(s.messages(t, s.other)))
}

// A message a change both saves and finds earlier is kept, with its ID:
// the earlier generations' sweep runs after the change's saves.
func TestSyncedMailSweepAfterSave(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("kept")}})
	kept := s.messages(t, s.account)["kept"].ID

	written := s.write(t, s.account, mailsync.Change{State: running(2), Saved: []mailsync.Message{syncedMessage("kept")}, DeleteEarlier: 1000})

	assert.Equal(t, 0, written.EarlierDeleted)
	assert.Equal(t, kept, s.messages(t, s.account)["kept"].ID)
}

func TestSyncedMailMailboxes(t *testing.T) {
	s := newSyncedMailTest(t)

	s.update(t, s.account, mailsync.Change{State: running(1), Mailboxes: gmailMailboxes})

	assert.Equal(t, gmailMailboxes, s.mailboxes(t, s.account))
	assert.Empty(t, s.mailboxes(t, s.other))
}

func TestSyncedMailMailboxRename(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1")}})
	receipts := s.mailboxID(t, s.account, "Label_1")
	renamed := []mailsync.Mailbox{
		{ProviderID: "INBOX", Name: "INBOX", Role: mailsync.RoleInbox},
		{ProviderID: "Label_1", Name: "Bills", Role: mailsync.RoleNone},
	}

	s.update(t, s.account, mailsync.Change{State: running(1), Mailboxes: renamed})

	assert.Equal(t, renamed, s.mailboxes(t, s.account))
	assert.Equal(t, receipts, s.mailboxID(t, s.account, "Label_1"), "a renamed mailbox keeps its ID")
	assert.Equal(t, []string{"INBOX", "Label_1"}, s.messages(t, s.account)["m1"].MailboxProviderIDs)
}

func TestSyncedMailMailboxesKept(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)

	s.update(t, s.account, mailsync.Change{State: running(1)})

	assert.Equal(t, gmailMailboxes, s.mailboxes(t, s.account), "nil mailboxes keep the saved ones")
}

func TestSyncedMailMailboxUnlisted(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1")}})
	inboxOnly := []mailsync.Mailbox{{ProviderID: "INBOX", Name: "INBOX", Role: mailsync.RoleInbox}}

	s.update(t, s.account, mailsync.Change{State: running(1), Mailboxes: inboxOnly})

	assert.Equal(t, inboxOnly, s.mailboxes(t, s.account))
	assert.Equal(t, []string{"INBOX"}, s.messages(t, s.account)["m1"].MailboxProviderIDs, "the message stays in its other mailboxes")
	assert.Equal(t, gmailMailboxes, s.mailboxes(t, s.other))
}

func TestSyncedMailMailboxesEmpty(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1")}})

	s.update(t, s.account, mailsync.Change{State: running(1), Mailboxes: []mailsync.Mailbox{}})

	assert.Empty(t, s.mailboxes(t, s.account), "an empty list deletes them all")
	assert.Empty(t, s.messages(t, s.account)["m1"].MailboxProviderIDs)
	assert.Equal(t, gmailMailboxes, s.mailboxes(t, s.other))
}

func TestSyncedMailState(t *testing.T) {
	s := newSyncedMailTest(t)
	at := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	complete := mailsync.SyncState{
		AccountID:      s.other, // ignored
		Status:         mailsync.StatusComplete,
		Position:       "18446744073709551615",
		Generation:     3,
		PagesSynced:    7,
		MessagesSynced: 640,
		LastSyncedAt:   &at,
	}

	saved := s.update(t, s.account, mailsync.Change{State: complete})

	complete.AccountID = s.account
	assert.Equal(t, complete, saved)
	var seen mailsync.SyncState
	_, err := s.db.UpdateSyncedMail(t.Context(), s.account, func(current mailsync.SyncState) (mailsync.Change, error) {
		seen = current
		return mailsync.Change{State: current}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, complete, seen, "update gets the saved state")
}

func TestSyncedMailStateWaiting(t *testing.T) {
	s := newSyncedMailTest(t)

	var seen mailsync.SyncState
	_, err := s.db.UpdateSyncedMail(t.Context(), s.account, func(current mailsync.SyncState) (mailsync.Change, error) {
		seen = current
		return mailsync.Change{State: current}, nil
	})
	require.NoError(t, err)

	assert.Equal(t, mailsync.SyncState{AccountID: s.account, Status: mailsync.StatusWaiting}, seen)
}

func TestSyncedMailStateFailed(t *testing.T) {
	s := newSyncedMailTest(t)
	at := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	failed := mailsync.SyncState{Status: mailsync.StatusFailed, Generation: 1, Failure: mailsync.FailureTokenUnreadable, FailedAt: &at}

	s.update(t, s.account, mailsync.Change{State: failed})

	failed.AccountID = s.account
	assert.Equal(t, failed, s.state(t))
}

// A complete account keeps a failed poll's failure: the full sync's status
// and the last sync's failure are apart.
func TestSyncedMailStatePollFailed(t *testing.T) {
	s := newSyncedMailTest(t)
	at := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	complete := mailsync.SyncState{Status: mailsync.StatusComplete, Position: "4096", Generation: 1, Failure: mailsync.FailureProvider, FailedAt: &at}

	s.update(t, s.account, mailsync.Change{State: complete})

	complete.AccountID = s.account
	assert.Equal(t, complete, s.state(t))
}

// state returns the account's saved sync state, read without a change.
func (s syncedMailTest) state(t *testing.T) mailsync.SyncState {
	t.Helper()
	var state mailsync.SyncState
	_, err := s.db.UpdateSyncedMail(t.Context(), s.account, func(current mailsync.SyncState) (mailsync.Change, error) {
		state = current
		return mailsync.Change{State: current}, nil
	})
	require.NoError(t, err)
	return state
}

// A position comes back as the provider wrote it, and an empty one clears
// it.
func TestSyncedMailPosition(t *testing.T) {
	s := newSyncedMailTest(t)
	for _, position := range []string{
		"0",
		`{"INBOX":{"uidvalidity":3857529045,"highestmodseq":90060115205545359}}`,
		"https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=" + strings.Repeat("x", 2048),
		"",
	} {
		state := mailsync.SyncState{Status: mailsync.StatusRunning, Position: position, Generation: 1}

		s.update(t, s.account, mailsync.Change{State: state})

		var seen mailsync.SyncState
		_, err := s.db.UpdateSyncedMail(t.Context(), s.account, func(current mailsync.SyncState) (mailsync.Change, error) {
			seen = current
			return mailsync.Change{State: current}, nil
		})
		require.NoError(t, err)
		assert.Equal(t, position, seen.Position)
	}
}

func TestSyncedMailStateRange(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	for name, state := range map[string]mailsync.SyncState{
		"a generation past int32": {Status: mailsync.StatusRunning, Generation: math.MaxInt32 + 1},
		"a negative page count":   {Status: mailsync.StatusRunning, Generation: 1, PagesSynced: -1},
		"a message count past it": {Status: mailsync.StatusRunning, Generation: 1, MessagesSynced: math.MaxInt32 + 1},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.db.UpdateSyncedMail(t.Context(), s.account, func(mailsync.SyncState) (mailsync.Change, error) {
				return mailsync.Change{State: state, Saved: []mailsync.Message{syncedMessage("m1")}}, nil
			})

			require.ErrorIs(t, err, errStateRange)
			assert.Empty(t, s.messages(t, s.account), "the change writes nothing")
		})
	}
}

func TestSyncedMailNotFound(t *testing.T) {
	s := newSyncedMailTest(t)
	_, err := s.conn.Exec(t.Context(), "DELETE FROM connected_accounts WHERE id = $1", s.account)
	require.NoError(t, err)

	called := false
	_, err = s.db.UpdateSyncedMail(t.Context(), s.account, func(mailsync.SyncState) (mailsync.Change, error) {
		called = true
		return mailsync.Change{}, nil
	})

	require.ErrorIs(t, err, mailsync.ErrNotFound)
	assert.False(t, called)
}

func TestSyncedMailUpdateError(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	refused := errors.New("refused")

	_, err := s.db.UpdateSyncedMail(t.Context(), s.account, func(mailsync.SyncState) (mailsync.Change, error) {
		return mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1")}}, refused
	})

	require.ErrorIs(t, err, refused)
	assert.Empty(t, s.messages(t, s.account))
}

func TestSyncedMailConcurrent(t *testing.T) {
	s := newSyncedMailTest(t)
	const updates = 8

	// Each update counts one more page than the state it was given.
	var wg sync.WaitGroup
	errs := make([]error, updates)
	for i := range updates {
		wg.Go(func() {
			_, errs[i] = s.db.UpdateSyncedMail(t.Context(), s.account, func(current mailsync.SyncState) (mailsync.Change, error) {
				current.PagesSynced++
				return mailsync.Change{State: current}, nil
			})
		})
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	var pages, otherPages int
	require.NoError(t, s.conn.QueryRow(t.Context(), "SELECT pages_synced FROM sync_states WHERE connected_account_id = $1", s.account).Scan(&pages))
	require.NoError(t, s.conn.QueryRow(t.Context(), "SELECT pages_synced FROM sync_states WHERE connected_account_id = $1", s.other).Scan(&otherPages))
	assert.Equal(t, updates, pages, "each update saw the one before")
	assert.Equal(t, 0, otherPages)
}

func TestSyncedMailDisconnect(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1")}})

	_, err := s.conn.Exec(t.Context(), "DELETE FROM connected_accounts WHERE id = $1", s.account)
	require.NoError(t, err)

	var left int
	require.NoError(t, s.conn.QueryRow(t.Context(), `
		SELECT (SELECT count(*) FROM messages) + (SELECT count(*) FROM message_mailboxes)
			+ (SELECT count(*) FROM mailboxes WHERE connected_account_id = $1)
			+ (SELECT count(*) FROM sync_states WHERE connected_account_id = $1)`, s.account).Scan(&left))
	assert.Equal(t, 0, left)
	assert.Len(t, s.mailboxes(t, s.other), len(gmailMailboxes))
}

// A disconnect while a write holds the account's sync state waits for the
// write, then deletes everything: the write's saves lock only rows under the
// sync state it holds, so neither deadlocks the other.
func TestSyncedMailWriteDisconnect(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	ctx := t.Context()
	disconnected := make(chan error, 1)

	_, writeErr := s.db.UpdateSyncedMail(ctx, s.account, func(current mailsync.SyncState) (mailsync.Change, error) {
		// The write holds the sync state; the disconnect starts and waits.
		go func() {
			_, err := s.db.pool.Exec(ctx, "DELETE FROM connected_accounts WHERE id = $1", s.account)
			disconnected <- err
		}()
		require.Eventually(t, func() bool {
			var blocked bool
			err := s.db.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE 'DELETE FROM connected_accounts%')").Scan(&blocked)
			return err == nil && blocked
		}, 3*time.Second, 10*time.Millisecond)
		// Saving a message checks the sync state it belongs to, which the
		// write holds. Checking the account's row, which the disconnect
		// holds, would deadlock.
		return mailsync.Change{State: current, Saved: []mailsync.Message{syncedMessage("m1")}}, nil
	})

	require.NoError(t, writeErr)
	require.NoError(t, <-disconnected)
	var left int
	require.NoError(t, s.conn.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM messages WHERE connected_account_id = $1)
			+ (SELECT count(*) FROM sync_states WHERE connected_account_id = $1)`, s.account).Scan(&left))
	assert.Equal(t, 0, left)
}

func TestSyncedMailChecks(t *testing.T) {
	s := newSyncedMailTest(t)
	failedAt := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	for name, change := range map[string]mailsync.Change{
		"failed without a failure":   {State: mailsync.SyncState{Status: mailsync.StatusFailed, Generation: 1}},
		"a failure without its time": {State: mailsync.SyncState{Status: mailsync.StatusRunning, Generation: 1, Failure: mailsync.FailureInternal}},
		"a time without a failure":   {State: mailsync.SyncState{Status: mailsync.StatusComplete, Generation: 1, FailedAt: &failedAt}},
		"a negative delete":          {State: running(1), DeleteEarlier: -1},
		"an unknown status":          {State: mailsync.SyncState{Status: "paused"}},
		"a save before a full sync":  {State: mailsync.SyncState{Status: mailsync.StatusWaiting}, Saved: []mailsync.Message{{ProviderID: "m1", ThreadID: "t1"}}},
		"a message without an ID":    {State: running(1), Saved: []mailsync.Message{{ThreadID: "t1"}}},
		"a mailbox without an ID":    {State: running(1), Mailboxes: []mailsync.Mailbox{{Name: "INBOX", Role: mailsync.RoleInbox}}},
		"an unknown role":            {State: running(1), Mailboxes: []mailsync.Mailbox{{ProviderID: "INBOX", Role: "starred"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.db.UpdateSyncedMail(t.Context(), s.account, func(mailsync.SyncState) (mailsync.Change, error) {
				return change, nil
			})
			assert.Error(t, err)
		})
	}
}

// keys returns messages' provider IDs, sorted.
func keys(messages map[string]mailsync.Message) []string {
	ids := make([]string, 0, len(messages))
	for id := range messages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// An update replaces a stored message's mailboxes and flags and nothing
// else; one for a message the account does not have, or another account's,
// is skipped, and the last of two for one message wins.
func TestSyncedMailUpdated(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1"), syncedMessage("m2")}})
	s.update(t, s.other, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("theirs")}})
	before := s.messages(t, s.account)

	s.update(t, s.account, mailsync.Change{State: running(1), Updated: []mailsync.MessageUpdate{
		{ProviderID: "m1", MailboxProviderIDs: []string{"INBOX"}, Seen: true},
		{ProviderID: "m2", MailboxProviderIDs: []string{"TRASH", "TRASH"}},
		{ProviderID: "m1", MailboxProviderIDs: []string{"Label_1"}, Seen: false, Flagged: true},
		{ProviderID: "never-stored", MailboxProviderIDs: []string{"INBOX"}},
		{ProviderID: "theirs", MailboxProviderIDs: []string{"SPAM"}},
	}})

	after := s.messages(t, s.account)
	assert.Equal(t, []string{"m1", "m2"}, keys(after))
	want := before["m1"]
	want.MailboxProviderIDs = []string{"Label_1"}
	want.Seen = false
	want.Flagged = true
	assert.Equal(t, want, after["m1"])
	assert.Equal(t, []string{"TRASH"}, after["m2"].MailboxProviderIDs)
	assert.False(t, after["m2"].Seen)
	assert.False(t, after["m2"].Flagged)
	assert.Equal(t, []string{"INBOX", "Label_1"}, s.messages(t, s.other)["theirs"].MailboxProviderIDs)
}

// An update to no mailbox leaves the message in none.
func TestSyncedMailUpdateEmpty(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1")}})

	s.update(t, s.account, mailsync.Change{State: running(1), Updated: []mailsync.MessageUpdate{{ProviderID: "m1", Seen: true}}})

	assert.Empty(t, s.messages(t, s.account)["m1"].MailboxProviderIDs)
}

// An update naming a mailbox the account lacks writes nothing.
func TestSyncedMailUpdateUnknown(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1")}})
	before := s.messages(t, s.account)

	_, err := s.db.UpdateSyncedMail(t.Context(), s.account, func(mailsync.SyncState) (mailsync.Change, error) {
		return mailsync.Change{State: running(1), Updated: []mailsync.MessageUpdate{
			{ProviderID: "m1", MailboxProviderIDs: []string{"Label_new"}, Seen: false},
		}}, nil
	})

	require.ErrorIs(t, err, mailsync.ErrUnknownMailbox)
	assert.Equal(t, before, s.messages(t, s.account))
}

// SyncState reads what the last change saved.
func TestSyncState(t *testing.T) {
	s := newSyncedMailTest(t)
	at := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	want := mailsync.SyncState{Status: mailsync.StatusComplete, Position: "4096", Generation: 2, PagesSynced: 3, MessagesSynced: 250, LastSyncedAt: &at}
	s.update(t, s.account, mailsync.Change{State: want})

	got, err := s.db.SyncState(t.Context(), s.account)

	require.NoError(t, err)
	want.AccountID = s.account
	assert.Equal(t, want, got)
	_, err = s.db.SyncState(t.Context(), s.account+100)
	require.ErrorIs(t, err, mailsync.ErrNotFound)
}

// The oldest message is the account's own, and none is reported for an
// account without messages.
func TestOldestMessage(t *testing.T) {
	s := newSyncedMailTest(t).withMailboxes(t)
	older := syncedMessage("older")
	older.InternalDate = older.InternalDate.Add(-48 * time.Hour)
	s.update(t, s.other, mailsync.Change{State: running(1), Saved: []mailsync.Message{older}})

	_, none, err := s.db.OldestMessage(t.Context(), s.account)
	require.NoError(t, err)
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{syncedMessage("m1"), syncedMessage("m2")}})
	oldest, found, err := s.db.OldestMessage(t.Context(), s.account)
	require.NoError(t, err)

	assert.False(t, none)
	assert.True(t, found)
	assert.Equal(t, syncedMessage("m1").InternalDate, oldest, "another account's older message is not this one's")
}

// Due syncs are the complete, waiting, and long-failed accounts whose
// member is active and which need no reconnect.
func TestDueSyncs(t *testing.T) {
	s := newSyncedMailTest(t)
	member := insertConnectingMember(t, s.conn, "second@example.com")
	deactivated := insertConnectingMember(t, s.conn, "gone@example.com")
	failedAt := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	states := map[string]mailsync.SyncState{
		"complete":      {Status: mailsync.StatusComplete, Position: "100", Generation: 1, Failure: mailsync.FailureProvider, FailedAt: &failedAt},
		"waiting":       {Status: mailsync.StatusWaiting},
		"running":       {Status: mailsync.StatusRunning, Generation: 1},
		"failed long":   {Status: mailsync.StatusFailed, Generation: 1, Failure: mailsync.FailureProvider, FailedAt: &failedAt},
		"failed recent": {Status: mailsync.StatusFailed, Generation: 1, Failure: mailsync.FailureProvider, FailedAt: new(failedAt.Add(time.Minute))},
		"reconnect":     {Status: mailsync.StatusComplete, Position: "100", Generation: 1},
		"deactivated":   {Status: mailsync.StatusComplete, Position: "100", Generation: 1},
	}
	ids := map[string]int64{}
	for name, state := range states {
		owner := member
		if name == "deactivated" {
			owner = deactivated
		}
		ids[name] = insertSyncingAccount(t, s.conn, owner, "sub-"+strings.ReplaceAll(name, " ", "-"))
		s.update(t, ids[name], mailsync.Change{State: state})
	}
	_, err := s.conn.Exec(t.Context(), "UPDATE connected_accounts SET needs_reconnect_since = now() WHERE id = $1", ids["reconnect"])
	require.NoError(t, err)
	_, err = s.conn.Exec(t.Context(), "UPDATE users SET deactivated_at = now() WHERE id = $1", deactivated)
	require.NoError(t, err)

	due, err := s.db.DueSyncs(t.Context(), failedAt)

	require.NoError(t, err)
	want := []mailsync.DueSync{
		{AccountID: s.account, Status: mailsync.StatusWaiting},
		{AccountID: s.other, Status: mailsync.StatusWaiting},
		{AccountID: ids["complete"], Status: mailsync.StatusComplete, Position: "100", Failure: mailsync.FailureProvider},
		{AccountID: ids["waiting"], Status: mailsync.StatusWaiting},
		{AccountID: ids["failed long"], Status: mailsync.StatusFailed, Failure: mailsync.FailureProvider},
	}
	slices.SortFunc(want, func(a, b mailsync.DueSync) int { return int(a.AccountID - b.AccountID) })
	assert.Equal(t, want, due)
}
