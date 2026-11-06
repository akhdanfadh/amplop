package passwordreset

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/systememail"
)

// noticeMemory answers ReadPasswordChangedNotice with state, or err.
type noticeMemory struct {
	state NoticeState
	err   error
	read  []int64
}

func (m *noticeMemory) ReadPasswordChangedNotice(_ context.Context, userID int64) (NoticeState, error) {
	m.read = append(m.read, userID)
	return m.state, m.err
}

// testNotices returns [Notices] over storage and mailer.
func testNotices(storage *noticeMemory, mailer *fakeMailer) *Notices {
	return NewNotices(storage, mailer, testPublicURL)
}

// After a reset, the account's owner is told at their current email that
// their password changed, so a reset they did not ask for does not go
// unnoticed.
func TestNoticeSent(t *testing.T) {
	storage := &noticeMemory{state: NoticeState{Email: "now@example.com", Active: true}}
	mailer := &fakeMailer{}

	// A time in another zone is still written in UTC.
	outcome, err := testNotices(storage, mailer).Send(t.Context(), testUser.ID, testNow.In(time.FixedZone("UTC+7", 7*60*60)))

	require.NoError(t, err)
	assert.Equal(t, OutcomeSent, outcome)
	assert.Equal(t, []int64{testUser.ID}, storage.read)
	require.Len(t, mailer.sent, 1)
	sent := mailer.sent[0]
	assert.Equal(t, "password changed notice", sent.Purpose)
	assert.Equal(t, "now@example.com", sent.To)
	assert.Equal(t, "Your inbok password was changed", sent.Subject)
	assert.False(t, sent.Private, "the notice holds no secret")
	assert.Contains(t, sent.Body, "The password of your inbok account at https://mail.example.com was changed on 3 November 2026 at 12:00 UTC")
	assert.Contains(t, sent.Body, "Contact your inbok administrator.")
}

// A user who is gone, or deactivated since, is not emailed.
func TestNoticeSkipped(t *testing.T) {
	for name, state := range map[string]NoticeState{
		"gone":        {},
		"deactivated": {Email: testUser.Email},
	} {
		t.Run(name, func(t *testing.T) {
			mailer := &fakeMailer{}

			outcome, err := testNotices(&noticeMemory{state: state}, mailer).Send(t.Context(), testUser.ID, testNow)

			require.NoError(t, err)
			assert.Equal(t, OutcomeNoRecipient, outcome)
			assert.Empty(t, mailer.sent)
		})
	}
}

// A send that may pass later is returned as [ErrNoticeNotSent], naming only
// where it failed; one that cannot ends with its outcome.
func TestNoticeSendFails(t *testing.T) {
	storage := &noticeMemory{state: NoticeState{Email: testUser.Email, Active: true}}
	mailer := &fakeMailer{err: &systememail.SendError{
		Reason: systememail.ReasonConnect, Err: errors.New("421 4.7.0 member@example.com try later"),
	}}

	_, err := testNotices(storage, mailer).Send(t.Context(), testUser.ID, testNow)
	require.ErrorIs(t, err, ErrNoticeNotSent)
	require.EqualError(t, err, "password changed notice not sent: failed at connect")

	mailer.err = systememail.ErrNotConfigured
	outcome, err := testNotices(storage, mailer).Send(t.Context(), testUser.ID, testNow)
	require.NoError(t, err)
	assert.Equal(t, OutcomeNotConfigured, outcome)
}

// A storage failure sends nothing and is returned.
func TestNoticeStorageError(t *testing.T) {
	storage := &noticeMemory{err: errors.New("database down")}
	mailer := &fakeMailer{}

	_, err := testNotices(storage, mailer).Send(t.Context(), testUser.ID, testNow)

	require.ErrorIs(t, err, storage.err)
	assert.Empty(t, mailer.sent)
}
