package conversation

import (
	"fmt"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"

	"amplop/internal/mailsync"
)

// The mailboxes of one account, by role.
var (
	inbox    = Mailbox{ID: uuid.MustParse("00000000-0000-4000-8000-000000000001"), Role: mailsync.RoleInbox, MadeBy: mailsync.MadeByProvider}
	sent     = Mailbox{ID: uuid.MustParse("00000000-0000-4000-8000-000000000002"), Role: mailsync.RoleSent, MadeBy: mailsync.MadeByProvider}
	spam     = Mailbox{ID: uuid.MustParse("00000000-0000-4000-8000-000000000003"), Role: mailsync.RoleJunk, MadeBy: mailsync.MadeByProvider}
	trash    = Mailbox{ID: uuid.MustParse("00000000-0000-4000-8000-000000000004"), Role: mailsync.RoleTrash, MadeBy: mailsync.MadeByProvider}
	drafts   = Mailbox{ID: uuid.MustParse("00000000-0000-4000-8000-000000000005"), Role: mailsync.RoleDrafts, MadeBy: mailsync.MadeByProvider}
	receipts = Mailbox{ID: uuid.MustParse("00000000-0000-4000-8000-000000000006"), MadeBy: mailsync.MadeByMember}
)

// sender returns an address for name at example.com.
func sender(name string) mailsync.Address {
	return mailsync.Address{Name: name, Address: name + "@example.com"}
}

// message returns an unread message from name, received day days after
// 1 November 2026, in mailboxes, with an ID that orders it by day.
func message(day int, name string, mailboxes ...Mailbox) Message {
	return Message{
		ID:        uuid.MustParse(fmt.Sprintf("10000000-0000-4000-8000-%012x", day)),
		Date:      time.Date(2026, 11, 1+day, 9, 0, 0, 0, time.UTC),
		Subject:   "Subject " + name,
		Preview:   "Preview " + name,
		From:      []mailsync.Address{sender(name)},
		Mailboxes: mailboxes,
	}
}

func TestDeriveReply(t *testing.T) {
	// Given a starred inbox message with a label, and my read reply in Sent.
	received := message(1, "asha", inbox, receipts)
	received.Flagged = true
	reply := message(2, "member", sent)
	reply.Subject = "Re: Subject asha"
	reply.Seen = true

	summary, ok := Derive([]Message{reply, received})

	// Then the row is dated by the reply, takes the first subject, both
	// senders, and every mailbox, and its newest message is the received
	// one, not the reply in Sent.
	assert.True(t, ok)
	assert.Equal(t, Summary{
		LatestDate:      reply.Date,
		Subject:         "Subject asha",
		Preview:         "Preview member",
		LatestFrom:      []mailsync.Address{sender("member")},
		Participants:    []mailsync.Address{sender("asha"), sender("member")},
		MessageCount:    2,
		Unread:          true,
		Flagged:         true,
		InAllMail:       true,
		NewestMessageID: received.ID,
		Mailboxes:       []uuid.UUID{inbox.ID, sent.ID, receipts.ID},
	}, summary)
}

// The newest message is the newest the row is made from that is not in
// Sent, else the newest of all.
func TestDeriveNewestMessage(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		want     int
	}{
		{"received last", []Message{message(1, "member", sent), message(2, "asha", inbox)}, 2},
		{"sent last", []Message{message(1, "asha", inbox), message(2, "member", sent), message(3, "member", sent)}, 1},
		{"only sent", []Message{message(1, "member", sent), message(2, "member", sent)}, 2},
		{"sent to self", []Message{message(1, "asha", inbox), message(2, "member", inbox, sent)}, 1},
		{"hidden left out", []Message{message(1, "asha", inbox), message(2, "blair", trash), message(3, "member", drafts)}, 1},
		{"only hidden", []Message{message(1, "asha", spam), message(2, "blair", trash)}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary, _ := Derive(tt.messages)

			assert.Equal(t, message(tt.want, "", inbox).ID, summary.NewestMessageID)
		})
	}
}

// A message in Spam or Trash counts only there: the row is made from the
// others, and its own labels place the conversation nowhere.
func TestDeriveTrashedReply(t *testing.T) {
	summary, ok := Derive([]Message{message(1, "asha", inbox), message(2, "blair", trash, receipts)})

	assert.True(t, ok)
	assert.Equal(t, "Preview asha", summary.Preview)
	assert.Equal(t, 1, summary.MessageCount)
	assert.Equal(t, []mailsync.Address{sender("asha")}, summary.Participants)
	assert.True(t, summary.InAllMail)
	assert.Equal(t, []uuid.UUID{inbox.ID, trash.ID}, summary.Mailboxes)
}

// Unread and starred come from the visible messages only, so a message in
// Spam or Trash marks the conversation in neither.
func TestDeriveHiddenFlags(t *testing.T) {
	read := message(1, "asha", inbox)
	read.Seen = true
	junk := message(2, "blair", spam)
	junk.Flagged = true
	trashed := message(3, "casey", trash)
	trashed.Flagged = true

	summary, ok := Derive([]Message{read, junk, trashed})

	assert.True(t, ok)
	assert.False(t, summary.Unread)
	assert.False(t, summary.Flagged)
	assert.Equal(t, 1, summary.MessageCount)
}

// A conversation with every message in Spam or Trash is made from them,
// and All mail and its labels leave it out.
func TestDeriveOnlyHidden(t *testing.T) {
	summary, ok := Derive([]Message{message(1, "asha", spam, receipts), message(2, "blair", trash)})

	assert.True(t, ok)
	assert.Equal(t, "Preview blair", summary.Preview)
	assert.Equal(t, 2, summary.MessageCount)
	assert.False(t, summary.InAllMail)
	assert.Equal(t, []uuid.UUID{spam.ID, trash.ID}, summary.Mailboxes)
}

func TestDeriveDrafts(t *testing.T) {
	summary, ok := Derive([]Message{message(1, "asha", inbox), message(2, "member", drafts)})
	_, onlyDraft := Derive([]Message{message(3, "member", drafts)})
	_, none := Derive(nil)

	assert.True(t, ok)
	assert.Equal(t, 1, summary.MessageCount, "a draft counts nowhere")
	assert.Equal(t, "Preview asha", summary.Preview)
	assert.Equal(t, []uuid.UUID{inbox.ID}, summary.Mailboxes)
	assert.False(t, onlyDraft, "a thread of drafts alone has no conversation")
	assert.False(t, none)
}

// An archived message with no label still makes a row, in All mail and
// no mailbox.
func TestDeriveArchived(t *testing.T) {
	read := message(1, "asha")
	read.Seen = true

	summary, ok := Derive([]Message{read})

	assert.True(t, ok)
	assert.False(t, summary.Unread)
	assert.True(t, summary.InAllMail)
	assert.Empty(t, summary.Mailboxes)
	assert.NotNil(t, summary.Mailboxes)
}

// Two messages at one time are ordered by ID, as lists order rows.
func TestDeriveTie(t *testing.T) {
	first := message(1, "asha", inbox)
	second := message(1, "blair", inbox)
	second.ID = uuid.MustParse("20000000-0000-4000-8000-000000000000")

	summary, _ := Derive([]Message{second, first})

	assert.Equal(t, "Subject asha", summary.Subject)
	assert.Equal(t, "Preview blair", summary.Preview)
}

func TestDeriveParticipants(t *testing.T) {
	// Given eleven senders, one writing again under another name and case,
	// a group with no address, and a message from two people.
	again := message(1, "asha", inbox)
	again.From = []mailsync.Address{{Name: "Asha K", Address: "ASHA@example.com"}}
	group := message(2, "group", inbox)
	group.From = []mailsync.Address{{Name: "undisclosed-recipients:;"}}
	pair := message(3, "b", inbox)
	pair.From = []mailsync.Address{sender("b"), sender("c")}
	messages := []Message{message(0, "asha", inbox), again, group, pair}
	for i, name := range []string{"d", "e", "f", "g", "h", "i", "j", "k"} {
		messages = append(messages, message(4+i, name, inbox))
	}

	summary, _ := Derive(messages)

	// Then each appears once, as first written, in date order, then by
	// their place in the header, and only the first 10.
	assert.Equal(t, []mailsync.Address{
		sender("asha"), {Name: "undisclosed-recipients:;"}, sender("b"), sender("c"),
		sender("d"), sender("e"), sender("f"), sender("g"), sender("h"), sender("i"),
	}, summary.Participants)
}

// A sender with no address is told apart from every address, however the
// address reads.
func TestDeriveSenderKinds(t *testing.T) {
	named := message(1, "named", inbox)
	named.From = []mailsync.Address{{Name: "alice@example.com"}}
	// The header's "name:alice"@example.com, as the header parser
	// unquotes it.
	quoted := message(2, "quoted", inbox)
	quoted.From = []mailsync.Address{{Address: "name:alice@example.com"}}

	forward, _ := Derive([]Message{named, quoted})
	quoted.Date, named.Date = named.Date, quoted.Date
	backward, _ := Derive([]Message{named, quoted})

	assert.Equal(t, []mailsync.Address{named.From[0], quoted.From[0]}, forward.Participants)
	assert.Equal(t, []mailsync.Address{quoted.From[0], named.From[0]}, backward.Participants)
}

func TestDeriveNoSender(t *testing.T) {
	anonymous := message(1, "asha", inbox)
	anonymous.From = nil

	summary, _ := Derive([]Message{anonymous})

	assert.NotNil(t, summary.LatestFrom)
	assert.Empty(t, summary.LatestFrom)
	assert.NotNil(t, summary.Participants)
	assert.Empty(t, summary.Participants)
}
