package account

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"inbok/internal/emailaddress"
	"inbok/internal/systememail"
)

// NoticeOutcome is how a recovery notice ended without an error worth
// retrying. It names no address, so logs and workflow records may hold it.
type NoticeOutcome string

const (
	NoticeSent          NoticeOutcome = "sent"
	NoticeNoRecipient   NoticeOutcome = "skipped: the account is missing or deactivated"
	NoticeNotConfigured NoticeOutcome = "skipped: system email is not configured"
	NoticeNotTested     NoticeOutcome = "skipped: system email has not passed a test"
	NoticeUnsendable    NoticeOutcome = "failed: the address or the saved password is unusable"
	NoticeRefused       NoticeOutcome = "failed: the mail server refused the address for good"
)

// ErrNoticeNotSent means a recovery notice's send failed in a way worth
// retrying. Its message names the failed step and whether the send is
// unconfirmed, never the server's reply.
var ErrNoticeNotSent = errors.New("recovery notice not sent")

// RecoveryNoticeState is what one recovery notice needs from storage: its
// recipient now, and the recovered account's email.
type RecoveryNoticeState struct {
	RecipientEmail  string
	RecipientActive bool   // deactivated_at is not set
	RecoveredEmail  string // empty when that account no longer exists
}

// recoveryNoticeStorage reads what a recovery notice needs.
type recoveryNoticeStorage interface {
	// ReadRecoveryNotice returns the [RecoveryNoticeState] of the notice to
	// the user with recipientID about the one with [recoveredID], who may be
	// the same. A missing recipient returns [ErrUserNotFound].
	ReadRecoveryNotice(ctx context.Context, recoveredID, recipientID int64) (RecoveryNoticeState, error)

	// ListActiveAdministrators returns the IDs of the administrators that
	// are not deactivated, oldest first.
	ListActiveAdministrators(ctx context.Context) ([]int64, error)
}

type noticeSender interface {
	Send(ctx context.Context, outgoing systememail.Outgoing) (int64, error)
}

// RecoveryNotices tells every administrator that host recovery replaced an
// administrator's password, so someone else running it does not go
// unnoticed: the recovered one gets a notice, the others an alert.
type RecoveryNotices struct {
	storage   recoveryNoticeStorage
	mailer    noticeSender
	publicURL *url.URL
}

// NewRecoveryNotices returns [RecoveryNotices] given storage, the
// [systememail.Mailer] that sends, and the public URL links start with.
func NewRecoveryNotices(storage recoveryNoticeStorage, mailer noticeSender, publicURL *url.URL) *RecoveryNotices {
	return &RecoveryNotices{storage: storage, mailer: mailer, publicURL: publicURL}
}

// Administrators returns the IDs of the administrators a recovery is
// reported to: every active one, the recovered one included. It returns
// storage's error.
func (n *RecoveryNotices) Administrators(ctx context.Context) ([]int64, error) {
	return n.storage.ListActiveAdministrators(ctx)
}

// Send tells the administrator with recipientID about the recovery of the
// one with recoveredID at recoveredAt, at the recipient's current address,
// with the system email settings: their own notice when the two are the
// same, an alert otherwise. It returns [NoticeSent], or the outcome that ends it unsent:
// the recipient now missing or deactivated, settings missing or untested,
// an address or saved password that cannot work, or a recipient the mail
// server refused with a 5xx reply. It returns an error,
// with no outcome, only for a failure worth retrying: storage's,
// [ErrNoticeNotSent] for a failed send, an unconfirmed one included, or
// ctx's error.
func (n *RecoveryNotices) Send(ctx context.Context, recoveredID int64, recoveredAt time.Time, recipientID int64) (NoticeOutcome, error) {
	// 1. Read the recipient now, not when the list was made: a retry or a
	// resumed run can come long after, and workers check that an account
	// is active before acting for it (ADR029).
	state, err := n.storage.ReadRecoveryNotice(ctx, recoveredID, recipientID)
	if errors.Is(err, ErrUserNotFound) {
		return NoticeNoRecipient, nil
	}
	if err != nil {
		return "", err
	}
	if !state.RecipientActive {
		return NoticeNoRecipient, nil
	}

	// 2. Write the recovered administrator's notice, or another's alert.
	outgoing := systememail.Outgoing{
		To: state.RecipientEmail,
		// Untested settings have not shown they work; a failing send would
		// only be retried until it gives up.
		RequireTested: true,
	}
	if recoveredID == recipientID {
		outgoing.Purpose = "recovery notice"
		outgoing.Subject = "Your inbok administrator account was recovered"
		outgoing.Body = n.ownBody(state.RecoveredEmail, recoveredAt)
	} else {
		outgoing.Purpose = "recovery alert"
		outgoing.Subject = "An inbok administrator account was recovered"
		outgoing.Body = n.alertBody(state.RecoveredEmail, recoveredAt)
	}

	// 3. Send it, and turn a failure retrying cannot fix into an outcome.
	_, err = n.mailer.Send(ctx, outgoing)
	switch {
	case err == nil:
		return NoticeSent, nil
	case errors.Is(err, systememail.ErrNotConfigured):
		return NoticeNotConfigured, nil
	case errors.Is(err, systememail.ErrNotTested):
		return NoticeNotTested, nil
	case errors.Is(err, systememail.ErrPasswordUnreadable), errors.Is(err, emailaddress.ErrInvalid):
		return NoticeUnsendable, nil
	}

	failed, isSendError := errors.AsType[*systememail.SendError](err)
	// A recipient refused with a 5xx reply would only be refused again and
	// hold up the next administrators. Other 5xx refusals are about inbok's
	// settings, which an administrator can fix while the send retries.
	if isSendError && failed.Reason == systememail.ReasonRecipient && failed.Permanent {
		return NoticeRefused, nil
	}

	// 4. Return any other failure, to be retried: it may pass, an
	// unconfirmed send included, and a second copy of this notice costs
	// less than none (ADR004).
	if isSendError {
		// The server's reply can quote what inbok sent it, the SMTP password
		// included, and DBOS keeps this error with the run; the mailer has
		// logged the reply already.
		state := ""
		if failed.Unconfirmed {
			state = ", unconfirmed"
		}
		return "", fmt.Errorf("%w: failed at %s%s", ErrNoticeNotSent, failed.Reason, state)
	}
	return "", err
}

// on returns the start of a sentence saying when at was, in UTC.
func on(at time.Time) string {
	return "On " + at.UTC().Format("2 January 2006 at 15:04 MST")
}

// ownBody returns the plain-text notice to the recovered administrator,
// whose address is email, recovered at recoveredAt.
func (n *RecoveryNotices) ownBody(email string, recoveredAt time.Time) string {
	signIn := *n.publicURL
	signIn.Path = "/sign-in"
	security := *n.publicURL
	security.Path = "/security"

	var b strings.Builder
	b.WriteString(on(recoveredAt))
	b.WriteString(", someone with access to the server that runs inbok at ")
	b.WriteString(n.publicURL.String())
	b.WriteString(" used host recovery on your administrator account, ")
	b.WriteString(email)
	b.WriteString(". It:\n\n")
	b.WriteString("- replaced your password with one they chose,\n")
	b.WriteString("- disconnected your authenticator app and deleted your backup codes,\n")
	b.WriteString("- signed you out everywhere.\n\n")
	b.WriteString("If you asked for this, sign in with the new password:\n\n")
	b.WriteString(signIn.String())
	b.WriteString("\n\n")
	b.WriteString("Then set up two-factor authentication again on the Security page. Until you do, your account has member access only:\n\n")
	b.WriteString(security.String())
	b.WriteString("\n\n")
	b.WriteString("If you did not ask for this, someone else may now control your administrator account. " +
		"Contact the people who run your inbok server right away: they can run host recovery again to help you get your account back.\n\n")
	b.WriteString("This email contains no password or code.\n")
	return b.String()
}

// alertBody returns the plain-text alert to another administrator about
// the recovery at recoveredAt of the account with email, empty when that
// account no longer exists.
func (n *RecoveryNotices) alertBody(email string, recoveredAt time.Time) string {
	account := "an administrator account"
	if email != "" {
		account = "the administrator account " + email
	}

	var b strings.Builder
	b.WriteString(on(recoveredAt))
	b.WriteString(", someone with access to the server that runs inbok at ")
	b.WriteString(n.publicURL.String())
	b.WriteString(" used host recovery on ")
	b.WriteString(account)
	b.WriteString(". It replaced that account's password with one they chose, ")
	b.WriteString("disconnected its authenticator app, deleted its backup codes, and signed it out everywhere.\n\n")
	b.WriteString("You are getting this because you are also an administrator. If you expected this, there is nothing to do.\n\n")
	b.WriteString("If you did not expect this, someone else may now control that account. " +
		"Check with its owner, and contact the people who run your inbok server right away.\n\n")
	b.WriteString("This email contains no password or code.\n")
	return b.String()
}
