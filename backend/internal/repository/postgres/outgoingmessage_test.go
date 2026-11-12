package postgres

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
	"inbok/internal/outgoingmessage"
)

// sendWorkflows saves each send job as a row of test_outgoing_message_jobs,
// in the caller's transaction, or returns err; account removals and full
// syncs go to [accountWorkflows].
type sendWorkflows struct {
	accountWorkflows
	err error
}

func (w sendWorkflows) EnqueueOutgoingMessage(ctx context.Context, tx pgx.Tx, messageID uuid.UUID) error {
	if w.err != nil {
		return w.err
	}
	_, err := tx.Exec(ctx, "INSERT INTO test_outgoing_message_jobs (message_id) VALUES ($1)", messageID)
	return err
}

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
	_, err := c.conn.Exec(t.Context(), "CREATE TABLE test_outgoing_message_jobs (message_id uuid NOT NULL)")
	require.NoError(t, err)
	c.db = c.db.WithWorkflows(sendWorkflows{})
	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)
	other, err := c.service.Connect(t.Context(), c.otherMember, blairIdentity, "refresh-2")
	require.NoError(t, err)
	return outgoingMessagesTest{
		connectedAccountsTest: c,
		messages:              outgoingmessage.NewService(c.db, c.service, time.Now),
		account:               account.ID,
		otherAccount:          other.ID,
	}
}

// draft is a draft every rule accepts, from the member's account, with a
// new key.
func (o outgoingMessagesTest) draft() outgoingmessage.Draft {
	return outgoingmessage.Draft{
		ConnectedAccountID: o.account,
		IdempotencyKey:     uuid.New(),
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
	assert.Regexp(t, `^<[^<>@]+@example\.com>$`, created.InbokMessageID)
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

	_, err := o.db.InsertOutgoingMessage(t.Context(), o.attempt(), outgoingmessage.Request{AccountID: o.account, Key: uuid.New()}, func(outgoingmessage.CreateState) (outgoingmessage.NewMessage, error) {
		return outgoingmessage.NewMessage{}, refused
	})

	require.ErrorIs(t, err, refused)
	assert.Equal(t, int64(0), o.countOutgoingMessages(t))
	assert.Equal(t, int64(0), o.countSendAttempts(t), "a refusal counts no attempt")
}

func TestOutgoingMessageSender(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	var got outgoingmessage.CreateState

	_, err := o.db.InsertOutgoingMessage(t.Context(), o.attempt(), outgoingmessage.Request{AccountID: o.account, Key: uuid.New()}, func(state outgoingmessage.CreateState) (outgoingmessage.NewMessage, error) {
		got = state
		return outgoingmessage.NewMessage{}, errors.New("stop")
	})

	require.Error(t, err)
	assert.Equal(t, o.account, got.Sender.ConnectedAccountID)
	assert.Equal(t, "alex@example.com", got.Sender.Email)
	assert.False(t, got.Sender.NeedsReconnect)
	// The sealed token, which the service checks a key still opens.
	require.NoError(t, o.service.CheckToken(o.member, o.account, got.Sender.Credential))
	assert.Empty(t, got.Attempts)
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

// Only a sent message has a sent time and a Gmail ID, and it has both and a
// thread, which a reply has from when it is saved.
func TestOutgoingMessageSentState(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)
	cases := []struct {
		name, set string
	}{
		{"waiting with a sent time", "sent_at = now()"},
		{"waiting with a Gmail ID", "provider_message_id = 'gmail-1'"},
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

// sendJobs lists the message IDs of the saved send jobs.
func (o outgoingMessagesTest) sendJobs(t *testing.T) []uuid.UUID {
	t.Helper()
	rows, err := o.conn.Query(t.Context(), "SELECT message_id FROM test_outgoing_message_jobs")
	require.NoError(t, err)
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	require.NoError(t, err)
	return ids
}

// Saving a message saves its send job with it.
func TestOutgoingMessageJob(t *testing.T) {
	o := newOutgoingMessagesTest(t)

	created, err := o.messages.Create(t.Context(), o.member, o.draft())

	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{created.ID}, o.sendJobs(t))
}

// A job that cannot be saved rolls the message back, so no message waits
// without one.
func TestOutgoingMessageJobFails(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	messages := outgoingmessage.NewService(o.db.WithWorkflows(sendWorkflows{err: errors.New("dbos down")}), o.service, time.Now)

	_, err := messages.Create(t.Context(), o.member, o.draft())

	require.ErrorContains(t, err, "dbos down")
	assert.Equal(t, int64(0), o.countOutgoingMessages(t))
}

// The job reads a message whoever owns it.
func TestOutgoingMessageToSend(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)

	read, err := o.db.ReadOutgoingMessageToSend(t.Context(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, created, read)

	_, err = o.db.ReadOutgoingMessageToSend(t.Context(), uuid.New())
	require.ErrorIs(t, err, outgoingmessage.ErrNotFound)
}

// A transition saves the state update decides, read under the row's lock.
func TestOutgoingMessageUpdate(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)
	sentAt := time.Date(2026, 11, 9, 10, 31, 0, 0, time.UTC)

	var seen outgoingmessage.Message
	saved, err := o.db.UpdateOutgoingMessage(t.Context(), created.ID, func(current outgoingmessage.SendState) (outgoingmessage.Transition, error) {
		seen = current.Message
		assert.False(t, current.SenderDeactivated)
		return outgoingmessage.Transition{
			Status: outgoingmessage.StatusSent,
			Sent:   outgoingmessage.Sent{ProviderMessageID: "gm-1", ProviderThreadID: "th-1"},
			SentAt: &sentAt,
		}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, created, seen)
	assert.Equal(t, outgoingmessage.StatusSent, saved.Status)
	assert.Equal(t, "gm-1", saved.ProviderMessageID)
	assert.Equal(t, "th-1", saved.ProviderThreadID)
	require.NotNil(t, saved.SentAt)
	assert.True(t, sentAt.Equal(*saved.SentAt))
	read, err := o.messages.Get(t.Context(), o.member, created.ID)
	require.NoError(t, err)
	assert.Equal(t, saved, read)
}

// Each failure reason the service gives is one the table takes.
func TestOutgoingMessageFailures(t *testing.T) {
	failures := []outgoingmessage.Failure{
		outgoingmessage.FailureRefused, outgoingmessage.FailureSendingLimit,
		outgoingmessage.FailureNeedsReconnect, outgoingmessage.FailureTokenUnreadable,
		outgoingmessage.FailureDeactivated, outgoingmessage.FailureSenderChanged,
		outgoingmessage.FailureGaveUp,
	}
	o := newOutgoingMessagesTest(t)
	for _, failure := range failures {
		created, err := o.messages.Create(t.Context(), o.member, o.draft())
		require.NoError(t, err)

		saved, err := o.db.UpdateOutgoingMessage(t.Context(), created.ID, func(outgoingmessage.SendState) (outgoingmessage.Transition, error) {
			return outgoingmessage.Transition{Status: outgoingmessage.StatusFailed, Failure: failure}, nil
		})

		require.NoError(t, err, string(failure))
		assert.Equal(t, failure, saved.Failure)
	}
}

// A sender's new address is saved on the account; the message keeps the
// address it was saved with.
func TestOutgoingSenderEmail(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)

	saved, err := o.db.UpdateOutgoingMessage(t.Context(), created.ID, func(outgoingmessage.SendState) (outgoingmessage.Transition, error) {
		return outgoingmessage.Transition{
			Status:      outgoingmessage.StatusFailed,
			Failure:     outgoingmessage.FailureSenderChanged,
			SenderEmail: "alex.renamed@example.com",
		}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, "alex@example.com", saved.From)
	accounts, err := o.service.List(t.Context(), o.member)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, "alex.renamed@example.com", accounts[0].Email)
}

// An error from update saves nothing.
func TestOutgoingUpdateError(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)
	refused := errors.New("refused")

	_, err = o.db.UpdateOutgoingMessage(t.Context(), created.ID, func(outgoingmessage.SendState) (outgoingmessage.Transition, error) {
		return outgoingmessage.Transition{}, refused
	})

	require.ErrorIs(t, err, refused)
	read, err := o.db.ReadOutgoingMessageToSend(t.Context(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, created, read)
}

// A message that went, e.g. with its account, cannot change.
func TestOutgoingMessageUpdateGone(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	called := false

	_, err := o.db.UpdateOutgoingMessage(t.Context(), uuid.New(), func(outgoingmessage.SendState) (outgoingmessage.Transition, error) {
		called = true
		return outgoingmessage.Transition{}, nil
	})

	require.ErrorIs(t, err, outgoingmessage.ErrNotFound)
	assert.False(t, called)
}

// waitForLock waits until another connection waits for a lock in the
// query named name.
func (o outgoingMessagesTest) waitForLock(t *testing.T, name string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var waiting bool
		err := o.conn.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE $1)`,
			"%-- name: "+name+" %").Scan(&waiting)
		require.NoError(t, err)
		return waiting
	}, 5*time.Second, 10*time.Millisecond)
}

// A send step that saves Gmail's new address and a disconnect of the same
// account at once both finish: the step locks the account before the
// message, the order a disconnect takes them in.
func TestOutgoingSenderEmailDisconnect(t *testing.T) {
	// Given a send step holding the message, about to save a new address.
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)
	locked := make(chan struct{})
	proceed := make(chan struct{})
	updated := make(chan error, 1)
	go func() {
		_, err := o.db.UpdateOutgoingMessage(t.Context(), created.ID, func(outgoingmessage.SendState) (outgoingmessage.Transition, error) {
			close(locked)
			<-proceed
			return outgoingmessage.Transition{
				Status:      outgoingmessage.StatusFailed,
				Failure:     outgoingmessage.FailureSenderChanged,
				SenderEmail: "alex.renamed@example.com",
			}, nil
		})
		updated <- err
	}()
	<-locked

	// When the member disconnects the account meanwhile.
	disconnected := make(chan error, 1)
	go func() {
		_, err := o.service.Disconnect(t.Context(), o.member, o.account)
		disconnected <- err
	}()
	o.waitForLock(t, "DeleteConnectedAccount")
	close(proceed)

	// Then the step saves, and the disconnect deletes the account and the
	// message after it, with no deadlock.
	require.NoError(t, <-updated)
	require.NoError(t, <-disconnected)
	assert.Equal(t, int64(0), o.countOutgoingMessages(t))
}

// A deactivation in progress holds the read of the member until it
// commits, so the send sees it (ADR029).
func TestOutgoingSenderDeactivated(t *testing.T) {
	// Given a deactivation that has locked the member and not committed.
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)
	deactivation, err := o.db.pool.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = deactivation.Rollback(context.Background()) })
	_, err = deactivation.Exec(t.Context(), "UPDATE users SET deactivated_at = now() WHERE id = $1", o.member)
	require.NoError(t, err)

	// When a send step updates the message meanwhile.
	seen := make(chan bool, 1)
	done := make(chan error, 1)
	go func() {
		_, err := o.db.UpdateOutgoingMessage(t.Context(), created.ID, func(state outgoingmessage.SendState) (outgoingmessage.Transition, error) {
			seen <- state.SenderDeactivated
			return outgoingmessage.Transition{Status: state.Status}, nil
		})
		done <- err
	}()

	// Then it waits for the deactivation, and sees it once committed.
	o.waitForLock(t, "LockOutgoingMessageMember")
	require.NoError(t, deactivation.Commit(t.Context()))
	require.NoError(t, <-done)
	assert.True(t, <-seen)
}

// A send, a connect, and a disconnect of the same account at once all
// finish: the send takes the member's lock before the message's, as the
// connect does before the account's and the disconnect the account's
// before the message's.
func TestOutgoingLockOrder(t *testing.T) {
	// Given a connect holding the member's lock, and a send waiting for it.
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)
	state := newConnectState(o.member, insertConnectingSession(t, o.conn, o.member), o.account)
	memberLocked := make(chan struct{})
	release := make(chan struct{})
	connectDone := make(chan error, 1)
	go func() {
		connectDone <- o.db.InsertConnectState(t.Context(), o.member, func([]time.Time) (connectedaccount.ConnectState, error) {
			close(memberLocked)
			<-release
			return state, nil
		})
	}()
	<-memberLocked
	sendDone := make(chan error, 1)
	go func() {
		_, err := o.db.UpdateOutgoingMessage(t.Context(), created.ID, func(state outgoingmessage.SendState) (outgoingmessage.Transition, error) {
			return outgoingmessage.Transition{Status: outgoingmessage.StatusSending}, nil
		})
		sendDone <- err
	}()
	o.waitForLock(t, "LockOutgoingMessageMember")

	// When the account is disconnected, and then the connect goes on.
	disconnectDone := make(chan error, 1)
	go func() {
		_, err := o.service.Disconnect(t.Context(), o.member, o.account)
		disconnectDone <- err
	}()
	var disconnectErr error
	select {
	case disconnectErr = <-disconnectDone:
	case <-time.After(3 * time.Second):
		t.Error("the disconnect waited for the send")
		close(release)
		disconnectErr = <-disconnectDone
		release = nil
	}
	if release != nil {
		close(release)
	}

	// Then the disconnect wins without waiting, the send finds the message
	// gone, and nothing deadlocks.
	require.NoError(t, disconnectErr)
	require.ErrorIs(t, <-sendDone, outgoingmessage.ErrNotFound)
	if connectErr := <-connectDone; connectErr != nil {
		assert.NotContains(t, connectErr.Error(), "deadlock")
	}
}

// attempt returns the member's attempt to send now.
func (o outgoingMessagesTest) attempt() outgoingmessage.Attempt {
	now := time.Now().UTC()
	return outgoingmessage.Attempt{UserID: o.member, At: now, Since: now.Add(-24 * time.Hour)}
}

// countSendAttempts returns how many send attempts are kept for anyone.
func (o outgoingMessagesTest) countSendAttempts(t *testing.T) int64 {
	t.Helper()
	var count int64
	require.NoError(t, o.conn.QueryRow(t.Context(), "SELECT count(*) FROM send_attempts").Scan(&count))
	return count
}

// A repeated key returns the first message: no second message, job, or
// attempt.
func TestOutgoingMessageRepeat(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	draft := o.draft()
	first, err := o.messages.Create(t.Context(), o.member, draft)
	require.NoError(t, err)

	again, err := o.messages.Create(t.Context(), o.member, draft)

	require.NoError(t, err)
	assert.Equal(t, first, again)
	assert.Equal(t, int64(1), o.countOutgoingMessages(t))
	assert.Equal(t, []uuid.UUID{first.ID}, o.sendJobs(t))
	assert.Equal(t, int64(1), o.countSendAttempts(t))
}

// A key the member used is refused before the account or the limit: here
// the account does not exist, which a check in the other order would report.
func TestOutgoingMessageKeyExists(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	draft := o.draft()
	_, err := o.messages.Create(t.Context(), o.member, draft)
	require.NoError(t, err)
	called := false

	_, err = o.db.InsertOutgoingMessage(t.Context(), o.attempt(), outgoingmessage.Request{AccountID: o.otherAccount + 100, Key: draft.IdempotencyKey}, func(outgoingmessage.CreateState) (outgoingmessage.NewMessage, error) {
		called = true
		return outgoingmessage.NewMessage{}, nil
	})

	require.ErrorIs(t, err, outgoingmessage.ErrKeyExists)
	assert.False(t, called)
}

// A key is the member's own: another member's request with it saves its own
// message, and neither member reads the other's by it.
func TestOutgoingKeyPerMember(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	draft := o.draft()
	mine, err := o.messages.Create(t.Context(), o.member, draft)
	require.NoError(t, err)
	draft.ConnectedAccountID = o.otherAccount

	theirs, err := o.messages.Create(t.Context(), o.otherMember, draft)

	require.NoError(t, err)
	assert.NotEqual(t, mine.ID, theirs.ID)
	read, _, err := o.db.ReadOutgoingMessageByKey(t.Context(), o.otherMember, draft.IdempotencyKey)
	require.NoError(t, err)
	assert.Equal(t, theirs.ID, read.ID)
}

// Two requests with one key at once save one message: the member's lock
// makes the second wait for the first to commit, then find it.
func TestOutgoingMessageRepeatRace(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	draft := o.draft()
	results := make(chan error, 2)
	ids := make(chan uuid.UUID, 2)

	for range 2 {
		go func() {
			message, err := o.messages.Create(context.WithoutCancel(t.Context()), o.member, draft)
			ids <- message.ID
			results <- err
		}()
	}

	require.NoError(t, <-results)
	require.NoError(t, <-results)
	first, second := <-ids, <-ids
	assert.Equal(t, first, second)
	assert.Equal(t, int64(1), o.countOutgoingMessages(t))
}

// A request with the same key that did not wait for the member's lock and
// committed first still leaves one message for the key: the second request
// gets ErrKeyExists from the key's primary key, not an internal error.
func TestOutgoingMessageKeyRace(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	draft := o.draft()
	tx, err := o.db.pool.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	var first uuid.UUID
	require.NoError(t, tx.QueryRow(t.Context(), `INSERT INTO outgoing_messages (connected_account_id, from_address,
		to_addresses, cc_addresses, bcc_addresses, subject, body, inbok_message_id)
		VALUES ($1, 'alex@example.com', '[{"name": "", "address": "a@example.com"}]', '[]', '[]', '', '', '<x@example.com>')
		RETURNING id`, o.account).Scan(&first))
	_, err = tx.Exec(t.Context(), "INSERT INTO outgoing_message_keys (user_id, idempotency_key, message_id) VALUES ($1, $2, $3)",
		o.member, draft.IdempotencyKey, first)
	require.NoError(t, err)
	done := make(chan error, 1)

	go func() {
		_, err := o.messages.Create(context.WithoutCancel(t.Context()), o.member, draft)
		done <- err
	}()
	o.waitForLock(t, "InsertOutgoingMessageKey")
	require.NoError(t, tx.Commit(t.Context()))

	require.ErrorIs(t, <-done, outgoingmessage.ErrKeyReused, "the first message has other content")
	assert.Equal(t, int64(1), o.countOutgoingMessages(t), "the second rolled back")
	assert.Equal(t, int64(0), o.countSendAttempts(t))
}

// The member's attempts are counted across their accounts, kept when an
// account is disconnected, and those no window counts are deleted.
func TestOutgoingMessageAttempts(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	old := time.Now().Add(-25 * time.Hour)
	_, err := o.conn.Exec(t.Context(), "INSERT INTO send_attempts (user_id, created_at) VALUES ($1, $2), ($3, $2)", o.member, old, o.otherMember)
	require.NoError(t, err)
	_, err = o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)

	_, err = o.service.Disconnect(t.Context(), o.member, o.account)
	require.NoError(t, err)

	var mine, others int64
	require.NoError(t, o.conn.QueryRow(t.Context(),
		"SELECT count(*) FILTER (WHERE user_id = $1), count(*) FILTER (WHERE user_id = $2) FROM send_attempts",
		o.member, o.otherMember).Scan(&mine, &others))
	assert.Equal(t, int64(1), mine, "the old attempt deleted, the new one kept after the disconnect")
	assert.Equal(t, int64(1), others, "another member's old attempt waits for their own send")
}

// Build gets the member's attempts in the window, and not another member's.
func TestOutgoingAttemptTimes(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	attempt := o.attempt()
	recent := attempt.At.Add(-time.Hour).Truncate(time.Microsecond)
	_, err := o.conn.Exec(t.Context(),
		"INSERT INTO send_attempts (user_id, created_at) VALUES ($1, $2), ($1, $3), ($4, $2)",
		o.member, recent, attempt.Since, o.otherMember)
	require.NoError(t, err)
	var got []time.Time

	_, err = o.db.InsertOutgoingMessage(t.Context(), attempt, outgoingmessage.Request{AccountID: o.account, Key: uuid.New()}, func(state outgoingmessage.CreateState) (outgoingmessage.NewMessage, error) {
		got = state.Attempts
		return outgoingmessage.NewMessage{}, errors.New("stop")
	})

	require.Error(t, err)
	require.Len(t, got, 1, "the one at Since no longer counts")
	assert.True(t, recent.Equal(got[0]))
}

func TestOutgoingDeactivated(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	_, err := o.conn.Exec(t.Context(), "UPDATE users SET deactivated_at = now() WHERE id = $1", o.member)
	require.NoError(t, err)

	_, err = o.messages.Create(t.Context(), o.member, o.draft())

	require.ErrorIs(t, err, outgoingmessage.ErrDeactivated)
	assert.Equal(t, int64(0), o.countOutgoingMessages(t))
}

// failedMessage saves a message from the member and ends it failed.
func (o outgoingMessagesTest) failedMessage(t *testing.T) outgoingmessage.Message {
	t.Helper()
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)
	_, err = o.conn.Exec(t.Context(), "UPDATE outgoing_messages SET status = 'failed', failure = 'gave-up' WHERE id = $1", created.ID)
	require.NoError(t, err)
	return created
}

// A copy is saved with its job and attempt, and the original names it; a
// second request returns the same copy.
func TestOutgoingMessageCopy(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	original := o.failedMessage(t)

	copied, err := o.messages.SendAgain(t.Context(), o.member, original.ID)
	require.NoError(t, err)
	again, err := o.messages.SendAgain(t.Context(), o.member, original.ID)
	require.NoError(t, err)

	assert.Equal(t, copied.ID, again.ID)
	assert.Equal(t, original.Content, copied.Content)
	assert.Equal(t, outgoingmessage.StatusWaiting, copied.Status)
	assert.ElementsMatch(t, []uuid.UUID{original.ID, copied.ID}, o.sendJobs(t))
	assert.Equal(t, int64(2), o.countSendAttempts(t), "the create and one copy")
	read, err := o.messages.Get(t.Context(), o.member, original.ID)
	require.NoError(t, err)
	assert.Equal(t, &copied.ID, read.SentAgainAs)
	assert.Equal(t, outgoingmessage.StatusFailed, read.Status)
}

// Build gets the original as saved and its account as it is now.
func TestOutgoingMessageCopyState(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	original := o.failedMessage(t)
	_, err := o.conn.Exec(t.Context(), "UPDATE connected_accounts SET email = 'alex.new@example.com' WHERE id = $1", o.account)
	require.NoError(t, err)
	var got outgoingmessage.CopyState

	_, err = o.db.InsertOutgoingMessageCopy(t.Context(), o.attempt(), original.ID, func(state outgoingmessage.CopyState) (outgoingmessage.NewMessage, error) {
		got = state
		return outgoingmessage.NewMessage{}, errors.New("stop")
	})

	require.Error(t, err)
	assert.Equal(t, original.ID, got.Original.ID)
	assert.Equal(t, outgoingmessage.StatusFailed, got.Original.Status)
	assert.Equal(t, "alex.new@example.com", got.Sender.Email)
	assert.Len(t, got.Attempts, 1)
	read, err := o.messages.Get(t.Context(), o.member, original.ID)
	require.NoError(t, err)
	assert.Nil(t, read.SentAgainAs, "a refusal links nothing")
}

func TestOutgoingCopyNotFound(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	original := o.failedMessage(t)

	_, err := o.messages.SendAgain(t.Context(), o.otherMember, original.ID)
	require.ErrorIs(t, err, outgoingmessage.ErrNotFound)

	_, err = o.messages.SendAgain(t.Context(), o.member, uuid.New())
	require.ErrorIs(t, err, outgoingmessage.ErrNotFound)
}

// Only a failed or unknown message names a copy.
func TestOutgoingMessageCopyCheck(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)
	other, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)

	_, err = o.conn.Exec(t.Context(), "UPDATE outgoing_messages SET sent_again_as = $1 WHERE id = $2", other.ID, created.ID)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23514", pgErr.Code, "check violation")
}

// The list is the member's newest messages first, without bodies.
func TestOutgoingMessageList(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	var created []outgoingmessage.Message
	for range 3 {
		message, err := o.messages.Create(t.Context(), o.member, o.draft())
		require.NoError(t, err)
		created = append(created, message)
	}
	otherDraft := o.draft()
	otherDraft.ConnectedAccountID = o.otherAccount
	_, err := o.messages.Create(t.Context(), o.otherMember, otherDraft)
	require.NoError(t, err)

	listed, err := o.db.ListOutgoingMessages(t.Context(), o.member, 2)

	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, created[2].ID, listed[0].ID)
	assert.Equal(t, created[1].ID, listed[1].ID)
	assert.Empty(t, listed[0].Body)
	assert.Equal(t, created[2].Subject, listed[0].Subject)
	assert.Equal(t, created[2].To, listed[0].To)
}

// answered saves a synchronized message, providerID, in accountID, and
// returns its ID.
func (o outgoingMessagesTest) answered(t *testing.T, accountID int64, providerID string) uuid.UUID {
	t.Helper()
	_, err := o.db.UpdateSyncedMail(t.Context(), accountID, func(mailsync.SyncState) (mailsync.Change, error) {
		return mailsync.Change{State: running(1), Mailboxes: gmailMailboxes, Saved: []mailsync.Message{syncedMessage(providerID)}}, nil
	})
	require.NoError(t, err)
	var id uuid.UUID
	require.NoError(t, o.conn.QueryRow(t.Context(),
		"SELECT id FROM messages WHERE connected_account_id = $1 AND provider_message_id = $2", accountID, providerID).Scan(&id))
	return id
}

// reply is a reply every rule accepts, from the member's account to
// answeredID, with a new key.
func (o outgoingMessagesTest) reply(answeredID uuid.UUID) outgoingmessage.Reply {
	draft := o.draft()
	return outgoingmessage.Reply{
		ConnectedAccountID: o.account,
		IdempotencyKey:     uuid.New(),
		AnsweredMessageID:  answeredID,
		To:                 draft.To,
		Cc:                 draft.Cc,
		Bcc:                draft.Bcc,
		Body:               draft.Body,
	}
}

// A reply is saved with the answered message's subject, threading, and
// thread, and read back with them.
func TestOutgoingReply(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	answeredID := o.answered(t, o.account, "m1")

	created, err := o.messages.Reply(t.Context(), o.member, o.reply(answeredID))
	require.NoError(t, err)

	assert.Equal(t, "Re: Quarterly report", created.Subject)
	assert.Equal(t, outgoingmessage.Threading{
		InReplyTo:  "<report-m1@example.co.id>",
		References: []string{"<thread@example.com>", "<request@example.com>", "<report-m1@example.co.id>"},
	}, created.Threading)
	assert.Equal(t, "thread-m1", created.ProviderThreadID)
	read, err := o.messages.Get(t.Context(), o.member, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created, read)
	listed, err := o.messages.List(t.Context(), o.member)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, created.Threading, listed[0].Threading)
}

// A new message is saved with no threading.
func TestOutgoingNoThreading(t *testing.T) {
	o := newOutgoingMessagesTest(t)

	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)

	read, err := o.messages.Get(t.Context(), o.member, created.ID)
	require.NoError(t, err)
	assert.Equal(t, outgoingmessage.Threading{References: []string{}}, read.Threading)
	assert.Empty(t, read.ProviderThreadID)
}

// Another member's message, or one gone, is not answered: nothing is saved
// or counted.
func TestOutgoingReplyNotFound(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	theirs := o.answered(t, o.otherAccount, "m1")

	for name, answeredID := range map[string]uuid.UUID{"another member's": theirs, "none": uuid.New()} {
		t.Run(name, func(t *testing.T) {
			_, err := o.messages.Reply(t.Context(), o.member, o.reply(answeredID))

			require.ErrorIs(t, err, outgoingmessage.ErrAnsweredNotFound)
			assert.Equal(t, int64(0), o.countOutgoingMessages(t))
			assert.Equal(t, int64(0), o.countSendAttempts(t))
		})
	}
}

// A repeated reply returns the first, even after a sync deleted the
// answered message; the key keeps which message it answered.
func TestOutgoingReplyRepeat(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	answeredID := o.answered(t, o.account, "m1")
	reply := o.reply(answeredID)
	first, err := o.messages.Reply(t.Context(), o.member, reply)
	require.NoError(t, err)
	_, err = o.conn.Exec(t.Context(), "DELETE FROM messages WHERE id = $1", answeredID)
	require.NoError(t, err)

	again, err := o.messages.Reply(t.Context(), o.member, reply)
	require.NoError(t, err)
	assert.Equal(t, first, again)

	reply.AnsweredMessageID = uuid.New()
	_, err = o.messages.Reply(t.Context(), o.member, reply)
	require.ErrorIs(t, err, outgoingmessage.ErrKeyReused)
	draft := o.draft()
	draft.IdempotencyKey = reply.IdempotencyKey
	draft.Subject = first.Subject
	_, err = o.messages.Create(t.Context(), o.member, draft)
	require.ErrorIs(t, err, outgoingmessage.ErrKeyReused)
	assert.Equal(t, int64(1), o.countOutgoingMessages(t))
}

// A reply keeps its thread through every status a send saves, and a send
// again copies its threading.
func TestOutgoingReplyThreadKept(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Reply(t.Context(), o.member, o.reply(o.answered(t, o.account, "m1")))
	require.NoError(t, err)

	for _, next := range []outgoingmessage.Transition{
		{Status: outgoingmessage.StatusSending},
		{Status: outgoingmessage.StatusWaiting},
		{Status: outgoingmessage.StatusFailed, Failure: outgoingmessage.FailureRefused},
	} {
		saved, err := o.db.UpdateOutgoingMessage(t.Context(), created.ID, func(outgoingmessage.SendState) (outgoingmessage.Transition, error) {
			return next, nil
		})
		require.NoError(t, err)
		assert.Equal(t, "thread-m1", saved.ProviderThreadID, string(next.Status))
	}

	copied, err := o.messages.SendAgain(t.Context(), o.member, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.Threading, copied.Threading)
	assert.Equal(t, "thread-m1", copied.ProviderThreadID)
	assert.Equal(t, created.Subject, copied.Subject)
}

// The table refuses an In-Reply-To without angle brackets, a missing
// References list, and one longer than 50; msgid.Writable, not the table,
// decides which msg-ids are written.
func TestOutgoingReplyColumns(t *testing.T) {
	o := newOutgoingMessagesTest(t)
	created, err := o.messages.Create(t.Context(), o.member, o.draft())
	require.NoError(t, err)
	cases := []struct {
		name, set string
	}{
		{"in-reply-to without brackets", "in_reply_to = 'a@example.com'"},
		{"references not a list", "internet_references = NULL"},
		{"too many references", "internet_references = array_fill('<a@example.com>'::text, ARRAY[51])"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := o.conn.Exec(t.Context(), "UPDATE outgoing_messages SET "+c.set+" WHERE id = $1", created.ID)

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Contains(t, []string{pgerrcode.CheckViolation, pgerrcode.NotNullViolation}, pgErr.Code)
		})
	}
}
