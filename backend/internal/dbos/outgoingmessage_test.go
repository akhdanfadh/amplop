package dbos

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/connectedaccount"
	"amplop/internal/encryption"
	"amplop/internal/outgoingmessage"
)

// fakeSends answers each step as set and counts the calls. checkGate, when
// set, holds Check until it is closed or the step's context ends.
type fakeSends struct {
	prepared   outgoingmessage.Preparation
	prepareErr error
	sent       outgoingmessage.Status
	sendErr    error
	checked    outgoingmessage.Status
	checkErr   error
	gaveUp     outgoingmessage.Status
	// giveUpFails is how many GiveUp calls fail before one succeeds.
	giveUpFails int

	checkStarted chan struct{}
	checkGate    chan struct{}

	mu        sync.Mutex
	calls     map[string]int
	historyID string
}

func newFakeSends() *fakeSends {
	return &fakeSends{
		prepared: outgoingmessage.Preparation{Status: outgoingmessage.StatusWaiting, HistoryID: "1200"},
		sent:     outgoingmessage.StatusSent,
		checked:  outgoingmessage.StatusSent,
		gaveUp:   outgoingmessage.StatusFailed,
		calls:    map[string]int{},
	}
}

func (f *fakeSends) count(step string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[step]++
}

// called returns how many times step ran.
func (f *fakeSends) called(step string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[step]
}

func (f *fakeSends) Prepare(context.Context, uuid.UUID) (outgoingmessage.Preparation, error) {
	f.count("prepare")
	return f.prepared, f.prepareErr
}

func (f *fakeSends) Send(context.Context, uuid.UUID) (outgoingmessage.Status, error) {
	f.count("send")
	return f.sent, f.sendErr
}

func (f *fakeSends) Check(ctx context.Context, _ uuid.UUID, historyID string) (outgoingmessage.Status, error) {
	f.count("check")
	f.mu.Lock()
	f.historyID = historyID
	f.mu.Unlock()
	if f.checkGate != nil {
		f.checkStarted <- struct{}{}
		select {
		case <-f.checkGate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return f.checked, f.checkErr
}

func (f *fakeSends) GiveUp(context.Context, uuid.UUID) (outgoingmessage.Status, error) {
	f.count("give up")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls["give up"] <= f.giveUpFails {
		return "", errors.New("database unavailable")
	}
	return f.gaveUp, nil
}

// sendWorker launches a worker with id on url sending with sends.
func sendWorker(t *testing.T, url, id string, sends outgoingMessages) *Worker {
	t.Helper()
	worker, err := NewWorker(url, id, Services{OutgoingMessages: sends}, discard)
	require.NoError(t, err)
	require.NoError(t, worker.Launch())
	return worker
}

// enqueueSend enqueues a send of a new message ID, as a client does, and
// returns its handle.
func enqueueSend(t *testing.T, worker *Worker) dbos.WorkflowHandle[outgoingmessage.Status] {
	t.Helper()
	id := uuid.New()
	handle, err := dbos.Enqueue[outgoingmessage.Status](worker.ctx, googleSendQueue, outgoingMessageWorkflow,
		OutgoingMessageInput{MessageID: id}, dbos.WithEnqueueWorkflowID(outgoingMessageWorkflowID(id)))
	require.NoError(t, err)
	return handle
}

// noCheckDelay makes the wait before a check short for one test.
func noCheckDelay(t *testing.T) {
	t.Helper()
	saved := checkDelay
	checkDelay = time.Millisecond
	t.Cleanup(func() { checkDelay = saved })
}

func TestOutgoingMessage(t *testing.T) {
	sends := newFakeSends()
	worker := sendWorker(t, migrated(t), "worker", sends)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	status, err := enqueueSend(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, outgoingmessage.StatusSent, status)
	assert.Equal(t, 1, sends.called("send"))
	assert.Zero(t, sends.called("check"))
}

// A message that ended before sending, e.g. its sender changed, is not
// sent.
func TestOutgoingMessageEnded(t *testing.T) {
	sends := newFakeSends()
	sends.prepared = outgoingmessage.Preparation{Status: outgoingmessage.StatusFailed}
	worker := sendWorker(t, migrated(t), "worker", sends)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	status, err := enqueueSend(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, outgoingmessage.StatusFailed, status)
	assert.Zero(t, sends.called("send"))
}

// An unanswered send is checked from the position noted before it, after
// the delay, and never sent again.
func TestOutgoingMessageUnanswered(t *testing.T) {
	noCheckDelay(t)
	sends := newFakeSends()
	sends.sent = outgoingmessage.StatusSending
	worker := sendWorker(t, migrated(t), "worker", sends)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	status, err := enqueueSend(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, outgoingmessage.StatusSent, status)
	assert.Equal(t, 1, sends.called("send"))
	assert.Equal(t, 1, sends.called("check"))
	assert.Equal(t, "1200", sends.historyID)
}

// A step that keeps failing is retried with its policy, then the message
// is given up.
func TestOutgoingMessageGivesUp(t *testing.T) {
	cases := []struct {
		step string
		set  func(*fakeSends)
		// runs is the first attempt and the policy's retries.
		runs int
	}{
		{"prepare", func(f *fakeSends) { f.prepareErr = errors.New("google unavailable") }, 6},
		{"send", func(f *fakeSends) { f.sendErr = errors.New("google unavailable") }, 6},
		{"check", func(f *fakeSends) {
			f.sent = outgoingmessage.StatusSending
			f.checkErr = errors.New("google unavailable")
		}, 4},
	}
	for _, c := range cases {
		t.Run(c.step, func(t *testing.T) {
			quickRetries(t, &sendRetries, &checkRetries)
			noCheckDelay(t)
			sends := newFakeSends()
			c.set(sends)
			worker := sendWorker(t, migrated(t), "worker", sends)
			t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

			status, err := enqueueSend(t, worker).GetResult(dbos.WithHandleTimeout(wait))

			require.NoError(t, err)
			assert.Equal(t, outgoingmessage.StatusFailed, status)
			assert.Equal(t, c.runs, sends.called(c.step))
			assert.Equal(t, 1, sends.called("give up"))
		})
	}
}

// Giving up that keeps failing, e.g. with the database down, is tried again
// after a wait until it is saved, so the message never stays waiting.
func TestOutgoingGiveUpFails(t *testing.T) {
	quickRetries(t, &sendRetries)
	saved := giveUpDelay
	giveUpDelay = time.Millisecond
	t.Cleanup(func() { giveUpDelay = saved })
	sends := newFakeSends()
	sends.prepareErr = errors.New("google unavailable")
	sends.giveUpFails = 8
	worker := sendWorker(t, migrated(t), "worker", sends)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	status, err := enqueueSend(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, outgoingmessage.StatusFailed, status)
	assert.Equal(t, 9, sends.called("give up"), "a first round of 6, then a second that succeeds on its third")
}

// A worker stopped during the check leaves the send pending; the next
// worker with its ID resumes at the check, never sending again.
func TestOutgoingMessageResume(t *testing.T) {
	// Given a worker stopped while the check of an unanswered send runs.
	noCheckDelay(t)
	url := migrated(t)
	sends := newFakeSends()
	sends.sent = outgoingmessage.StatusSending
	sends.checkStarted = make(chan struct{}, 2)
	sends.checkGate = make(chan struct{})
	first := sendWorker(t, url, "worker", sends)
	handle := enqueueSend(t, first)
	receive(t, sends.checkStarted, "check started")
	require.NoError(t, first.Shutdown())
	assert.Equal(t, "PENDING", workflowStatus(t, url, handle.GetWorkflowID()))

	// When another worker with the same ID launches.
	close(sends.checkGate)
	second := sendWorker(t, url, "worker", sends)
	t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })

	// Then the send ends from the check, sent once.
	resumed, err := dbos.RetrieveWorkflow[outgoingmessage.Status](second.ctx, handle.GetWorkflowID())
	require.NoError(t, err)
	status, err := resumed.GetResult(dbos.WithHandleTimeout(wait))
	require.NoError(t, err)
	assert.Equal(t, outgoingmessage.StatusSent, status)
	assert.Equal(t, 1, sends.called("prepare"))
	assert.Equal(t, 1, sends.called("send"))
	assert.Equal(t, 2, sends.called("check"))
}

// Sends are retried 5 more times, waiting 2, 4, 8, 16, then 30 seconds,
// and checks 3 more times, waiting 30 seconds, 1, then 2 minutes, after 2
// minutes; giving up that keeps failing waits 15 minutes between rounds, as
// the backend README promises.
func TestSendRetryPolicy(t *testing.T) {
	assert.Equal(t, retries{attempts: 5, base: 2 * time.Second, ceiling: 30 * time.Second}, sendRetries)
	assert.Equal(t, retries{attempts: 3, base: 30 * time.Second, ceiling: 2 * time.Minute}, checkRetries)
	assert.Equal(t, 2*time.Minute, checkDelay)
	assert.Equal(t, 15*time.Minute, giveUpDelay)
}

// A save that fails at commit, after the job was saved in its transaction,
// leaves neither the message nor its job.
func TestClientOutgoingRollback(t *testing.T) {
	// Given a connected account, and messages that fail as they commit.
	db, conn, _ := clientStack(t)
	var member int64
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT id FROM users WHERE email = 'admin@example.com'").Scan(&member))
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	accounts := connectedaccount.NewService(db, ring)
	account, err := accounts.Connect(t.Context(), member,
		connectedaccount.Identity{Subject: "100000000000000000001", Email: "alex@example.com"}, "refresh-secret-1")
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), `CREATE FUNCTION reject_message() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'message rejected'; END $$`)
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), "CREATE CONSTRAINT TRIGGER reject_write AFTER INSERT ON outgoing_messages DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_message()")
	require.NoError(t, err)

	// When the member sends a message.
	_, err = outgoingmessage.NewService(db, accounts, time.Now).Create(t.Context(), member, outgoingmessage.Draft{
		ConnectedAccountID: account.ID,
		IdempotencyKey:     uuid.New(),
		Content:            outgoingmessage.Content{To: []string{"blair@example.com"}, Subject: "Lunch", Body: "Friday?"},
	})

	// Then it fails, and neither the message nor a send is saved.
	require.ErrorContains(t, err, "message rejected")
	var messages, sends int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM outgoing_messages").Scan(&messages))
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM dbos.workflow_status WHERE name = $1", outgoingMessageWorkflow).Scan(&sends))
	assert.Zero(t, messages)
	assert.Zero(t, sends)
}

// Saving a message saves its send on the Google send queue, under the
// message's ID, with only that ID as input.
func TestClientOutgoingMessage(t *testing.T) {
	// Given a member with a connected account, through storage that saves
	// workflows with a Client.
	db, conn, _ := clientStack(t)
	var member int64
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT id FROM users WHERE email = 'admin@example.com'").Scan(&member))
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	accounts := connectedaccount.NewService(db, ring)
	account, err := accounts.Connect(t.Context(), member,
		connectedaccount.Identity{Subject: "100000000000000000001", Email: "alex@example.com"}, "refresh-secret-1")
	require.NoError(t, err)

	// When the member sends a message.
	message, err := outgoingmessage.NewService(db, accounts, time.Now).Create(t.Context(), member, outgoingmessage.Draft{
		ConnectedAccountID: account.ID,
		IdempotencyKey:     uuid.New(),
		Content:            outgoingmessage.Content{To: []string{"blair@example.com"}, Subject: "Lunch", Body: "Friday?"},
	})
	require.NoError(t, err)

	// Then its send waits on the Google send queue, named for it, holding
	// neither content nor token.
	var id, input, queue string
	require.NoError(t, conn.QueryRow(t.Context(), `SELECT s.workflow_uuid, i.inputs, s.queue_name FROM dbos.workflow_input i
		JOIN dbos.workflow_status s USING (workflow_uuid) WHERE s.name = $1`, outgoingMessageWorkflow).Scan(&id, &input, &queue))
	assert.Equal(t, outgoingMessageWorkflowID(message.ID), id)
	assert.Equal(t, googleSendQueue, queue)
	decoded, err := base64.StdEncoding.DecodeString(input)
	require.NoError(t, err)
	assert.Contains(t, string(decoded), message.ID.String())
	assert.NotContains(t, string(decoded), "blair")
	assert.NotContains(t, string(decoded), "refresh")
}
