package postgres

import (
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/mailsync"
	"inbok/internal/search"
)

// A member's index sources are their messages among the IDs, with
// addresses as stored, mailboxes by inbok's IDs in order, and whether
// parts are stored; another member's message, and an unknown ID, are
// left out.
func TestIndexSources(t *testing.T) {
	s := newMessagePartsTest(t)
	ids := s.save(t, "m1", "m2")
	_, err := s.db.SaveMessageParts(t.Context(), s.account, ids[1], storedTree())
	require.NoError(t, err)
	stranger := insertConnectingMember(t, s.conn, "stranger@example.com")
	theirAccount := insertSyncingAccount(t, s.conn, stranger, "sub-stranger")
	s.update(t, theirAccount, mailsync.Change{State: running(1), Mailboxes: gmailMailboxes, Saved: []mailsync.Message{syncedMessage("theirs")}})
	theirs := s.messages(t, theirAccount)["theirs"].ID

	sources, err := s.db.IndexSources(t.Context(), s.member, []uuid.UUID{ids[0], ids[1], theirs, uuid.New()})
	require.NoError(t, err)

	mailboxes := []uuid.UUID{s.mailboxID(t, s.account, "INBOX"), s.mailboxID(t, s.account, "Label_1")}
	if mailboxes[0].String() > mailboxes[1].String() {
		mailboxes[0], mailboxes[1] = mailboxes[1], mailboxes[0]
	}
	m := syncedMessage("m1")
	want := search.Source{
		ID:           ids[0],
		AccountID:    s.account,
		InternalDate: time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC),
		Subject:      m.Subject,
		From:         m.From,
		To:           m.To,
		CC:           m.CC,
		MailboxIDs:   mailboxes,
	}
	withParts := want
	withParts.ID = ids[1]
	withParts.HasParts = true
	assert.ElementsMatch(t, []search.Source{want, withParts}, sources)
}
