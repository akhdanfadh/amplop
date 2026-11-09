package postgres

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/outgoingmessage"
)

// outgoingMessagesTest is a migrated database with two members, each with
// a connected account, and the outgoing message service on it.
type outgoingMessagesTest struct {
	connectedAccountsTest
	messages     *outgoingmessage.Service
	account      int64
	otherAccount int64
}

func newOutgoingMessagesTest(t *testing.T) outgoingMessagesTest {
	t.Helper()
	c := newConnectedAccountsTest(t)
	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)
	other, err := c.service.Connect(t.Context(), c.otherMember, blairIdentity, "refresh-2")
	require.NoError(t, err)
	return outgoingMessagesTest{
		connectedAccountsTest: c,
		messages:              outgoingmessage.NewService(c.db),
		account:               account.ID,
		otherAccount:          other.ID,
	}
}

// draft is a draft every rule accepts, from the member's account.
func (o outgoingMessagesTest) draft() outgoingmessage.Draft {
	return outgoingmessage.Draft{
		ConnectedAccountID: o.account,
		Content: outgoingmessage.Content{
			To:      []string{"Ann@example.com"},
			Cc:      []string{},
			Bcc:     []string{"cat@example.com", "dan@example.com"},
			Subject: "Lunch on Friday",
			Body:    "Are you free?",
		},
	}
}

// countOutgoingMessages returns how many messages are saved for anyone.
func (o outgoingMessagesTest) countOutgoingMessages(t *testing.T) int64 {
	t.Helper()
	var count int64
	require.NoError(t, o.conn.QueryRow(t.Context(), "SELECT count(*) FROM outgoing_messages").Scan(&count))
	return count
}

func TestOutgoingMessageStorage(t *testing.T) {
	o := newOutgoingMessagesTest(t)

	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)

	assert.Equal(t, o.account, created.ConnectedAccountID)
	assert.Equal(t, "alex@example.com", created.From)
	assert.Equal(t, o.draft().Content, created.Content)
	assert.Regexp(t, `^<[^<>@]+@example\.com>$`, created.InternetMessageID)
	assert.Equal(t, outgoingmessage.StatusWaiting, created.Status)
	assert.Empty(t, created.Failure)
	assert.Empty(t, created.ProviderMessageID)
	assert.Nil(t, created.SentAt)
	assert.WithinDuration(t, time.Now(), created.CreatedAt, time.Minute)
	read, err := o.messages.Get(t.Context(), o.member, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created, read)
}

func TestOutgoingMessageNotOwned(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	draft := o.draft()
	draft.ConnectedAccountID = o.otherAccount

	_, err := o.messages.Create(t.Context(), o.member, draft)

	require.ErrorIs(t, err, outgoingmessage.ErrAccountNotFound)
	assert.Equal(t, int64(0), o.countOutgoingMessages(t))
}

func TestOutgoingMessageNoAccount(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	draft := o.draft()
	draft.ConnectedAccountID = o.otherAccount + 100

	_, err := o.messages.Create(t.Context(), o.member, draft)

	require.ErrorIs(t, err, outgoingmessage.ErrAccountNotFound)
}

func TestOutgoingMessageStaleToken(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	_, err := o.conn.Exec(t.Context(), "UPDATE connected_accounts SET needs_reconnect_since = now() WHERE id = $1", o.account)
	require.NoError(t, err)

	_, err = o.messages.Create(t.Context(), o.member, o.draft())

	require.ErrorIs(t, err, outgoingmessage.ErrNeedsReconnect)
	assert.Equal(t, int64(0), o.countOutgoingMessages(t))
}

func TestOutgoingMessageBuildError(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	refused := errors.New("refused")

	_, err := o.db.InsertOutgoingMessage(t.Context(), o.member, o.account, func(outgoingmessage.Sender) (outgoingmessage.NewMessage, error) {
		return outgoingmessage.NewMessage{}, refused
	})

	require.ErrorIs(t, err, refused)
	assert.Equal(t, int64(0), o.countOutgoingMessages(t))
}

func TestOutgoingMessageSender(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	var got outgoingmessage.Sender

	_, err := o.db.InsertOutgoingMessage(t.Context(), o.member, o.account, func(sender outgoingmessage.Sender) (outgoingmessage.NewMessage, error) {
		got = sender
		return outgoingmessage.NewMessage{}, errors.New("stop")
	})

	require.Error(t, err)
	assert.Equal(t, outgoingmessage.Sender{ConnectedAccountID: o.account, Email: "alex@example.com"}, got)
}

func TestOutgoingMessageNotYours(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)

	_, err = o.messages.Get(t.Context(), o.otherMember, created.ID)

	require.ErrorIs(t, err, outgoingmessage.ErrNotFound)
}

func TestOutgoingMessageDisconnect(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)

	_, err = o.service.Disconnect(t.Context(), o.member, o.account)
	require.NoError(t, err)

	_, err = o.messages.Get(t.Context(), o.member, created.ID)
	require.ErrorIs(t, err, outgoingmessage.ErrNotFound)
	assert.Equal(t, int64(0), o.countOutgoingMessages(t))
}

// A reconnect that changes the account's email leaves a saved message's
// sender as it was.
func TestOutgoingMessageRenamed(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)
	renamed := connectedaccount.Identity{Subject: alexIdentity.Subject, Email: "alex.renamed@example.com"}

	_, err = o.service.Reconnect(t.Context(), o.member, o.account, renamed, "refresh-3")
	require.NoError(t, err)

	read, err := o.messages.Get(t.Context(), o.member, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "alex@example.com", read.From)
}

// Only a sent message has a sent time and a Gmail ID, and it has both.
func TestOutgoingMessageSentState(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)
	cases := []struct {
		name, set string
	}{
		{"waiting with a sent time", "sent_at = now()"},
		{"waiting with a Gmail ID", "provider_message_id = 'gmail-1'"},
		{"waiting with a thread ID", "provider_thread_id = 'thread-1'"},
		{"sent without a Gmail ID", "status = 'sent', sent_at = now(), provider_thread_id = 'thread-1'"},
		{"sent without a thread ID", "status = 'sent', sent_at = now(), provider_message_id = 'gmail-1'"},
		{"sent without a sent time", "status = 'sent', provider_message_id = 'gmail-1', provider_thread_id = 'thread-1'"},
		{"failed without a reason", "status = 'failed'"},
		{"waiting with a reason", "failure = 'refused'"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := o.conn.Exec(t.Context(), "UPDATE outgoing_messages SET "+c.set+" WHERE id = $1", created.ID)

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Equal(t, "23514", pgErr.Code, "check violation")
		})
	}

	_, err = o.conn.Exec(t.Context(), `UPDATE outgoing_messages SET status = 'sent', sent_at = now(),
		provider_message_id = 'gmail-1', provider_thread_id = 'thread-1' WHERE id = $1`, created.ID)
	require.NoError(t, err)
}

// Recipients are kept as JMAP's EmailAddress, as synchronized mail keeps
// them.
func TestOutgoingMessageJSON(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)

	var to, cc, bcc string
	require.NoError(t, o.conn.QueryRow(t.Context(),
		"SELECT to_addresses::text, cc_addresses::text, bcc_addresses::text FROM outgoing_messages WHERE id = $1",
		created.ID).Scan(&to, &cc, &bcc))

	assert.JSONEq(t, `[{"name": "", "address": "Ann@example.com"}]`, to)
	assert.JSONEq(t, `[]`, cc)
	assert.JSONEq(t, `[{"name": "", "address": "cat@example.com"}, {"name": "", "address": "dan@example.com"}]`, bcc)
}

func TestOutgoingMessageNoSuchID(t *testing.T) {
	o := newOutgoingMessagesTest(t)

	_, err := o.messages.Get(t.Context(), o.member, uuid.New())

	require.ErrorIs(t, err, outgoingmessage.ErrNotFound)
}
