package postgres

import (
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/mailsync"
	"amplop/internal/messagecontent"
)

// bodiesSince and bodiesFailedSince bound the messages needing bodies in
// these tests, and bodiesFilter keeps them, leaving out spam and trash.
var (
	bodiesSince       = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	bodiesFailedSince = time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	bodiesFilter      = messagecontent.DueFilter{
		Since: bodiesSince, FailedSince: bodiesFailedSince, ExcludedRoles: mailsync.MetadataOnlyRoles(),
	}
)

// saveDated saves a message with providerID, received at, in mailboxes,
// to the account, and returns its ID.
func (s messagePartsTest) saveDated(t *testing.T, providerID string, at time.Time, mailboxes ...string) uuid.UUID {
	t.Helper()
	message := syncedMessage(providerID)
	message.InternalDate = at
	message.MailboxProviderIDs = mailboxes
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: []mailsync.Message{message}})
	return s.messages(t, s.account)[providerID].ID
}

// pendingIDs returns the provider IDs the account's messages needing
// bodies list, up to limit.
func (s messagePartsTest) pendingIDs(t *testing.T, limit int) []string {
	t.Helper()
	pending, err := s.db.MessagesNeedingBodies(t.Context(), s.account, bodiesFilter, limit)
	require.NoError(t, err)
	ids := []string{}
	for _, message := range pending {
		ids = append(ids, message.ProviderID)
	}
	return ids
}

// The messages needing bodies are those in the window, newest first,
// without parts, in no junk or trash mailbox, and not failed lately.
func TestMessagesNeedingBodies(t *testing.T) {
	s := newMessagePartsTest(t)
	day := func(n int) time.Time { return bodiesSince.AddDate(0, 0, n) }
	s.saveDated(t, "old", day(-1), "INBOX")
	s.saveDated(t, "edge", day(0), "INBOX")
	s.saveDated(t, "newest", day(40), "INBOX")
	s.saveDated(t, "unlabelled", day(30))
	s.saveDated(t, "spam", day(30), "SPAM")
	s.saveDated(t, "trash", day(30), "INBOX", "TRASH")
	parts := s.saveDated(t, "parts", day(30), "INBOX")
	failed := s.saveDated(t, "failed", day(30), "INBOX")
	failedLong := s.saveDated(t, "failed-long-ago", day(20), "INBOX")
	_, err := s.db.SaveMessageParts(t.Context(), s.account, parts, storedTree())
	require.NoError(t, err)
	require.NoError(t, s.db.MarkBodyFailed(t.Context(), s.account, failed, bodiesFailedSince))
	require.NoError(t, s.db.MarkBodyFailed(t.Context(), s.account, failedLong, bodiesFailedSince.Add(-time.Second)))
	other := syncedMessage("other-account")
	other.InternalDate = day(10)
	s.update(t, s.other, mailsync.Change{State: running(1), Saved: []mailsync.Message{other}})

	assert.Equal(t, []string{"newest", "unlabelled", "failed-long-ago", "edge"}, s.pendingIDs(t, 10))
	assert.Equal(t, []string{"newest", "unlabelled"}, s.pendingIDs(t, 2))

	// The roles left out are the caller's: without trash, its message is due.
	onlyJunk := bodiesFilter
	onlyJunk.ExcludedRoles = []mailsync.Role{mailsync.RoleJunk}
	pending, err := s.db.MessagesNeedingBodies(t.Context(), s.account, onlyJunk, 10)
	require.NoError(t, err)
	assert.Len(t, pending, 5, "the four, and the one in trash")
}

// An account's bodies are due once its full sync is complete and a message
// needs one, unless it needs a reconnect or its member is deactivated.
func TestAccountsWithDueBodies(t *testing.T) {
	s := newMessagePartsTest(t)
	due := func() []int64 {
		t.Helper()
		accounts, err := s.db.AccountsWithDueBodies(t.Context(), bodiesFilter)
		require.NoError(t, err)
		return accounts
	}
	complete := mailsync.SyncState{Status: mailsync.StatusComplete, Position: "4096", Generation: 1}
	message := s.saveDated(t, "m1", bodiesSince.AddDate(0, 0, 1), "INBOX")
	other := syncedMessage("m2")
	other.InternalDate = bodiesSince.AddDate(0, 0, 1)
	s.update(t, s.other, mailsync.Change{State: running(1), Saved: []mailsync.Message{other}})
	assert.Empty(t, due(), "while the full syncs run")

	s.update(t, s.account, mailsync.Change{State: complete})
	s.update(t, s.other, mailsync.Change{State: complete})
	assert.Equal(t, []int64{min(s.account, s.other), max(s.account, s.other)}, due())

	_, err := s.conn.Exec(t.Context(), "UPDATE connected_accounts SET needs_reconnect_since = now() WHERE id = $1", s.other)
	require.NoError(t, err)
	assert.Equal(t, []int64{s.account}, due(), "not an account that needs a reconnect")

	_, err = s.db.SaveMessageParts(t.Context(), s.account, message, storedTree())
	require.NoError(t, err)
	assert.Empty(t, due(), "once every message has its body")

	// Messages the fetch would not take leave the account not due, as the
	// two queries repeat the same conditions.
	s.saveDated(t, "old", bodiesSince.Add(-time.Second), "INBOX")
	s.saveDated(t, "spam", bodiesSince.AddDate(0, 0, 2), "SPAM")
	s.saveDated(t, "trash", bodiesSince.AddDate(0, 0, 2), "INBOX", "TRASH")
	failed := s.saveDated(t, "failed", bodiesSince.AddDate(0, 0, 2), "INBOX")
	require.NoError(t, s.db.MarkBodyFailed(t.Context(), s.account, failed, bodiesFailedSince))
	assert.Empty(t, due(), "for messages the fetch would not take")
	assert.Empty(t, s.pendingIDs(t, 10))

	_, err = s.conn.Exec(t.Context(), "UPDATE connected_accounts SET needs_reconnect_since = NULL WHERE id = $1", s.other)
	require.NoError(t, err)
	_, err = s.conn.Exec(t.Context(), "UPDATE users SET deactivated_at = now() WHERE id = $1", s.member)
	require.NoError(t, err)
	assert.Empty(t, due(), "not a deactivated member's")
}

// A failed fetch is recorded on the account's own message only.
func TestMarkBodyFailed(t *testing.T) {
	s := newMessagePartsTest(t)
	message := s.save(t, "m1")[0]
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

	require.NoError(t, s.db.MarkBodyFailed(t.Context(), s.account, message, at))

	var failedAt time.Time
	require.NoError(t, s.conn.QueryRow(t.Context(), "SELECT body_failed_at FROM messages WHERE id = $1", message).Scan(&failedAt))
	assert.True(t, at.Equal(failedAt))
	require.ErrorIs(t, s.db.MarkBodyFailed(t.Context(), s.other, message, at), messagecontent.ErrMessageNotFound)
	require.ErrorIs(t, s.db.MarkBodyFailed(t.Context(), s.account, uuid.New(), at), messagecontent.ErrMessageNotFound)
}
