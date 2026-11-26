package passwordreset

import (
	"context"
	"errors"
	"net/url"
	"time"

	"amplop/internal/systememail"
)

const (
	noticePurpose = "password changed notice"
	noticeSubject = "Your amplop password was changed"
)

// ErrNoticeNotSent means a password changed notice's send failed in a way
// worth retrying. It carries only where the send failed, never the
// server's reply.
var ErrNoticeNotSent = errors.New("password changed notice not sent")

// NoticeState is what a password changed notice reads about its user.
type NoticeState struct {
	Email  string // the user's current email; empty: the user is gone
	Active bool
}

// noticeStorage reads what a password changed notice needs.
type noticeStorage interface {
	// ReadPasswordChangedNotice returns the [NoticeState] of the user with
	// userID, the zero state when that user is gone.
	ReadPasswordChangedNotice(ctx context.Context, userID int64) (NoticeState, error)
}

// Notices tells users their password was changed through a reset link;
// the password changed notice job calls [Notices.Send].
type Notices struct {
	storage   noticeStorage
	mailer    mailer
	publicURL *url.URL
}

// NewNotices returns [Notices] given storage, the [systememail.Mailer] that
// sends, and the public URL the notice names.
func NewNotices(storage noticeStorage, mailer mailer, publicURL *url.URL) *Notices {
	return &Notices{storage: storage, mailer: mailer, publicURL: publicURL}
}

// Send emails the user with userID, at their current email, that their
// password changed at changedAt, and returns [OutcomeSent]. It sends nothing
// and returns [OutcomeNoRecipient] when the user is gone or deactivated. A send
// that retrying cannot fix returns its outcome, e.g.
// [OutcomeNotConfigured]. It returns storage's error, or an error wrapping
// [ErrNoticeNotSent] for a send worth retrying, such as an unconfirmed one,
// after which the user may get the notice twice.
func (n *Notices) Send(ctx context.Context, userID int64, changedAt time.Time) (Outcome, error) {
	// The job may run long after the reset, so it reads the address now.
	state, err := n.storage.ReadPasswordChangedNotice(ctx, userID)
	if err != nil {
		return "", err
	}
	if state.Email == "" || !state.Active {
		return OutcomeNoRecipient, nil
	}

	_, err = n.mailer.Send(ctx, systememail.Outgoing{
		Purpose: noticePurpose, To: state.Email, Subject: noticeSubject, Body: n.noticeBody(changedAt),
	})
	return sendOutcome(err, ErrNoticeNotSent)
}

// noticeBody returns the plain-text email telling an account's owner that
// its password changed at changedAt.
func (n *Notices) noticeBody(changedAt time.Time) string {
	return "The password of your amplop account at " + n.publicURL.String() + " was changed on " +
		changedAt.UTC().Format("2 January 2006 at 15:04 UTC") + ", using a password reset link sent to this email address.\n\n" +
		"If you did this, you don't need to do anything.\n\n" +
		"If you didn't, someone else may be able to read your email. Contact your amplop administrator.\n"
}
