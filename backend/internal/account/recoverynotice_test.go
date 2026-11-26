package account

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/emailaddress"
	"amplop/internal/systememail"
)

var noticeRecoveredAt = time.Date(2026, 11, 3, 11, 16, 0, 0, time.UTC)

// IDs of the accounts in these tests: [recoveredID] was recovered,
// otherAdminID is another administrator.
const (
	recoveredID  int64 = 7
	otherAdminID int64 = 8
)

// noticeUser is an account in [noticeMemory].
type noticeUser struct {
	email  string
	active bool
}

// noticeMemory answers a recovery notice's reads from users by ID and the
// active administrators, or with readErr.
type noticeMemory struct {
	users   map[int64]noticeUser
	admins  []int64
	readErr error
}

func (m *noticeMemory) ReadRecoveryNotice(_ context.Context, recoveredID, recipientID int64) (RecoveryNoticeState, error) {
	if m.readErr != nil {
		return RecoveryNoticeState{}, m.readErr
	}
	recipient, ok := m.users[recipientID]
	if !ok {
		return RecoveryNoticeState{}, ErrUserNotFound
	}
	return RecoveryNoticeState{
		RecipientEmail:  recipient.email,
		RecipientActive: recipient.active,
		RecoveredEmail:  m.users[recoveredID].email,
	}, nil
}

func (m *noticeMemory) ListActiveAdministrators(context.Context) ([]int64, error) {
	return m.admins, m.readErr
}

// noticeMailer records what it is asked to send and answers with err.
type noticeMailer struct {
	sent []systememail.Outgoing
	err  error
}

func (m *noticeMailer) Send(_ context.Context, outgoing systememail.Outgoing) (int64, error) {
	m.sent = append(m.sent, outgoing)
	return 1, m.err
}

// twoAdmins returns storage with the recovered administrator and another,
// both active.
func twoAdmins() *noticeMemory {
	return &noticeMemory{
		users: map[int64]noticeUser{
			recoveredID:  {email: "admin@example.com", active: true},
			otherAdminID: {email: "other@example.com", active: true},
		},
		admins: []int64{recoveredID, otherAdminID},
	}
}

func testNotices(storage *noticeMemory, mailer *noticeMailer) *RecoveryNotices {
	return NewRecoveryNotices(storage, mailer, &url.URL{Scheme: "https", Host: "mail.example.com"})
}

// The notice goes to every active administrator storage lists.
func TestRecoveryNoticeAdministrators(t *testing.T) {
	admins, err := testNotices(twoAdmins(), &noticeMailer{}).Administrators(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []int64{recoveredID, otherAdminID}, admins)
}

// A failed list is returned, to be retried.
func TestRecoveryNoticeAdministratorsError(t *testing.T) {
	lost := errors.New("connection lost")

	_, err := testNotices(&noticeMemory{readErr: lost}, &noticeMailer{}).Administrators(t.Context())

	require.ErrorIs(t, err, lost)
}

// Each way a send can end has its outcome; only failures worth retrying
// are errors, unconfirmed sends included.
func TestRecoveryNoticeSend(t *testing.T) {
	lost := errors.New("connection lost")
	cases := map[string]struct {
		sendErr error
		want    NoticeOutcome
		retry   error
	}{
		"sent":                      {want: NoticeSent},
		"not configured":            {sendErr: systememail.ErrNotConfigured, want: NoticeNotConfigured},
		"not tested":                {sendErr: systememail.ErrNotTested, want: NoticeNotTested},
		"password unreadable":       {sendErr: systememail.ErrPasswordUnreadable, want: NoticeUnsendable},
		"invalid address":           {sendErr: emailaddress.ErrInvalid, want: NoticeUnsendable},
		"settings unreadable":       {sendErr: lost, retry: lost},
		"server failed":             {sendErr: &systememail.SendError{Reason: systememail.ReasonConnect, Err: lost}, retry: ErrNoticeNotSent},
		"unconfirmed":               {sendErr: &systememail.SendError{Reason: systememail.ReasonTimeout, Err: lost, Unconfirmed: true}, retry: ErrNoticeNotSent},
		"recipient refused":         {sendErr: &systememail.SendError{Reason: systememail.ReasonRecipient, Err: lost, Permanent: true}, want: NoticeRefused},
		"recipient refused for now": {sendErr: &systememail.SendError{Reason: systememail.ReasonRecipient, Err: lost}, retry: ErrNoticeNotSent},
		"sign-in refused":           {sendErr: &systememail.SendError{Reason: systememail.ReasonAuth, Err: lost, Permanent: true}, retry: ErrNoticeNotSent},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			mailer := &noticeMailer{err: c.sendErr}

			outcome, err := testNotices(twoAdmins(), mailer).Send(t.Context(), recoveredID, noticeRecoveredAt, recoveredID)

			if c.retry != nil {
				require.ErrorIs(t, err, c.retry)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, c.want, outcome)
			require.Len(t, mailer.sent, 1)
			assert.True(t, mailer.sent[0].RequireTested, "untested settings send nothing")
		})
	}
}

// A failed send's error names the step and whether it is unconfirmed, never
// the server's reply, which can quote what amplop sent it, e.g. the SMTP
// password; DBOS keeps the error with the run.
func TestRecoveryNoticeSendErrorText(t *testing.T) {
	reply := errors.New("535 authentication failed for password hunter2")
	mailer := &noticeMailer{err: &systememail.SendError{Reason: systememail.ReasonAuth, Err: reply, Unconfirmed: true}}

	_, err := testNotices(twoAdmins(), mailer).Send(t.Context(), recoveredID, noticeRecoveredAt, otherAdminID)

	require.ErrorIs(t, err, ErrNoticeNotSent)
	assert.NotContains(t, err.Error(), "hunter2")
	assert.Contains(t, err.Error(), "auth")
	assert.Contains(t, err.Error(), "unconfirmed")
}

// Each send reads the recipient again, since a retry or a resumed run can
// come long after the list: a deactivated or missing administrator gets
// nothing, and a changed address gets the notice.
func TestRecoveryNoticeSendRechecks(t *testing.T) {
	cases := map[string]struct {
		recipient *noticeUser // nil: missing
		want      NoticeOutcome
		to        []string
	}{
		"address changed": {recipient: &noticeUser{email: "moved@example.com", active: true}, want: NoticeSent, to: []string{"moved@example.com"}},
		"deactivated":     {recipient: &noticeUser{email: "other@example.com"}, want: NoticeNoRecipient},
		"missing":         {want: NoticeNoRecipient},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			storage := twoAdmins()
			delete(storage.users, otherAdminID)
			if c.recipient != nil {
				storage.users[otherAdminID] = *c.recipient
			}
			mailer := &noticeMailer{}

			outcome, err := testNotices(storage, mailer).Send(t.Context(), recoveredID, noticeRecoveredAt, otherAdminID)
			require.NoError(t, err)

			assert.Equal(t, c.want, outcome)
			var to []string
			for _, sent := range mailer.sent {
				to = append(to, sent.To)
			}
			assert.Equal(t, c.to, to)
		})
	}
}

// The recovered administrator learns what changed, how to finish if it was
// expected, and what to do if not. It holds no secret, so logs may quote
// the server's reply.
func TestRecoveryNoticeOwnMessage(t *testing.T) {
	mailer := &noticeMailer{}

	_, err := testNotices(twoAdmins(), mailer).Send(t.Context(), recoveredID, noticeRecoveredAt, recoveredID)
	require.NoError(t, err)

	require.Len(t, mailer.sent, 1)
	sent := mailer.sent[0]
	assert.Equal(t, "admin@example.com", sent.To)
	assert.Equal(t, "recovery notice", sent.Purpose)
	assert.False(t, sent.Private)
	assert.Equal(t, "Your amplop administrator account was recovered", sent.Subject)
	for _, want := range []string{
		"3 November 2026 at 11:16 UTC", "admin@example.com", "replaced your password",
		"https://mail.example.com/sign-in", "https://mail.example.com/security",
		"If you did not ask for this",
	} {
		assert.Contains(t, sent.Body, want)
	}
}

// Another administrator learns which account was recovered and when, and
// what to do if it was unexpected.
func TestRecoveryNoticeAlertMessage(t *testing.T) {
	mailer := &noticeMailer{}

	_, err := testNotices(twoAdmins(), mailer).Send(t.Context(), recoveredID, noticeRecoveredAt, otherAdminID)
	require.NoError(t, err)

	require.Len(t, mailer.sent, 1)
	sent := mailer.sent[0]
	assert.Equal(t, "other@example.com", sent.To)
	assert.Equal(t, "recovery alert", sent.Purpose)
	assert.Equal(t, "An amplop administrator account was recovered", sent.Subject)
	for _, want := range []string{
		"3 November 2026 at 11:16 UTC", "admin@example.com", "you are also an administrator",
		"If you did not expect this",
	} {
		assert.Contains(t, sent.Body, want)
	}
	assert.NotContains(t, sent.Body, "/security", "the steps to finish are the recovered administrator's")
}

// An alert still goes out when the recovered account no longer exists,
// naming no address.
func TestRecoveryNoticeAlertRecoveredGone(t *testing.T) {
	storage := twoAdmins()
	delete(storage.users, recoveredID)
	mailer := &noticeMailer{}

	outcome, err := testNotices(storage, mailer).Send(t.Context(), recoveredID, noticeRecoveredAt, otherAdminID)
	require.NoError(t, err)

	assert.Equal(t, NoticeSent, outcome)
	require.Len(t, mailer.sent, 1)
	assert.Contains(t, mailer.sent[0].Body, "used host recovery on an administrator account.")
}
