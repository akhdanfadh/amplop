package postgres

import (
	"slices"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/connectedaccount"
	"amplop/internal/label"
	"amplop/internal/mailsync"
)

// nestedMailboxes are [threadMailboxes] with 2026 under Receipts.
var nestedMailboxes = append(slices.Clone(threadMailboxes),
	mailsync.Mailbox{ProviderID: "Label_2", Name: "2026", ProviderParentID: "Label_1", MadeBy: mailsync.MadeByMember})

// An account's labels are its mailboxes with no role that the member
// made, each with its parent; an account of another member is not found.
func TestListLabels(t *testing.T) {
	c := newConversationReadTest(t)
	c.update(t, c.account, mailsync.Change{State: running(1), Mailboxes: nestedMailboxes})
	receipts := c.mailboxID(t, c.account, "Label_1")

	labels, err := c.db.ListLabels(t.Context(), c.member, c.account)

	require.NoError(t, err)
	assert.Equal(t, []label.Label{
		{ID: c.mailboxID(t, c.account, "Label_2"), AccountID: c.account, ProviderID: "Label_2", Name: "2026", Parent: receipts},
		{ID: receipts, AccountID: c.account, ProviderID: "Label_1", Name: "Receipts"},
	}, labels)

	_, err = c.db.ListLabels(t.Context(), c.member, c.strangerAccount)
	require.ErrorIs(t, err, connectedaccount.ErrNotFound)
}

// An account with no labels lists none, and is found.
func TestListLabelsNone(t *testing.T) {
	c := newSyncedMailTest(t)

	labels, err := c.db.ListLabels(t.Context(), c.member, c.account)

	require.NoError(t, err)
	assert.Empty(t, labels)
}

// A label's account is found only for the member's own label.
func TestReadLabelAccount(t *testing.T) {
	c := newConversationReadTest(t)

	accountID, err := c.db.ReadLabelAccount(t.Context(), c.member, c.mailboxID(t, c.account, "Label_1"))
	require.NoError(t, err)
	assert.Equal(t, c.account, accountID)

	for name, id := range map[string]uuid.UUID{
		"another member's": c.mailboxID(t, c.strangerAccount, "Label_1"),
		"the inbox":        c.mailboxID(t, c.account, "INBOX"),
		"no such label":    uuid.New(),
	} {
		_, err := c.db.ReadLabelAccount(t.Context(), c.member, id)
		assert.ErrorIs(t, err, label.ErrNotFound, name)
	}
}

// Saving the mailboxes keeps a listed mailbox's ID, deletes one no longer
// listed with its conversations' membership, and keeps the sync state.
func TestSaveMailboxes(t *testing.T) {
	c := newConversationReadTest(t)
	c.update(t, c.account, mailsync.Change{State: running(1), Mailboxes: nestedMailboxes})
	c.saveTo(t, c.account, threadMessage("m1", "t1", 1, "casey", "INBOX", "Label_1", "Label_2"))
	receipts := c.mailboxID(t, c.account, "Label_1")
	before := c.state(t)

	renamed := slices.Clone(threadMailboxes)
	renamed[slices.IndexFunc(renamed, func(m mailsync.Mailbox) bool { return m.ProviderID == "Label_1" })].Name = "Bills"
	require.NoError(t, c.db.SaveMailboxes(t.Context(), c.account, renamed))

	labels, err := c.db.ListLabels(t.Context(), c.member, c.account)
	require.NoError(t, err)
	assert.Equal(t, []label.Label{{ID: receipts, AccountID: c.account, ProviderID: "Label_1", Name: "Bills"}}, labels)
	assert.Equal(t, []string{"INBOX", "Label_1"}, c.conversations(t, c.account)["t1"].Mailboxes)
	assert.Equal(t, before, c.state(t))
}

// An account gone is not found.
func TestSaveMailboxesAccountGone(t *testing.T) {
	c := newSyncedMailTest(t)

	err := c.db.SaveMailboxes(t.Context(), c.account+1000, threadMailboxes)

	require.ErrorIs(t, err, connectedaccount.ErrNotFound)
}
