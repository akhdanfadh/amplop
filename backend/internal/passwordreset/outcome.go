package passwordreset

import (
	"errors"
	"fmt"

	"inbok/internal/emailaddress"
	"inbok/internal/systememail"
)

// Outcome is how a password reset email job ended without an error worth
// retrying.
type Outcome string

const (
	OutcomeSent          Outcome = "sent"
	OutcomeNotEligible   Outcome = "skipped: no account, or one that may not reset or is over its limits"
	OutcomeNoRecipient   Outcome = "skipped: the user is gone or deactivated"
	OutcomeNotConfigured Outcome = "skipped: system email is not configured"
	OutcomeUnsendable    Outcome = "failed: the address or the saved password is unusable"
	OutcomeRefused       Outcome = "failed: the mail server refused the address for good"
)

// Failed reports whether o means the job could not send what it should
// have, e.g. because system email is not configured, as opposed to sending
// it or having no one to send to.
func (o Outcome) Failed() bool {
	return o != OutcomeSent && o != OutcomeNotEligible && o != OutcomeNoRecipient
}

// sendOutcome returns what a send that returned err means for its job:
// [OutcomeSent] for nil, an outcome for a failure retrying cannot fix, an
// error wrapping notSent for a [systememail.SendError] worth retrying, an
// unconfirmed one included, and any other error unchanged.
func sendOutcome(err, notSent error) (Outcome, error) {
	switch {
	case err == nil:
		return OutcomeSent, nil
	case errors.Is(err, systememail.ErrNotConfigured):
		return OutcomeNotConfigured, nil
	case errors.Is(err, systememail.ErrPasswordUnreadable), errors.Is(err, emailaddress.ErrInvalid):
		return OutcomeUnsendable, nil
	}

	failed, isSendError := errors.AsType[*systememail.SendError](err)
	if isSendError && failed.Reason == systememail.ReasonRecipient && failed.Permanent {
		return OutcomeRefused, nil
	}
	if !isSendError {
		return "", err
	}

	// DBOS logs and keeps this error with the run, and the server's reply
	// can quote the message, e.g. a live reset link; the mailer has logged
	// where the send failed.
	state := ""
	if failed.Unconfirmed {
		state = ", unconfirmed"
	}
	return "", fmt.Errorf("%w: failed at %s%s", notSent, failed.Reason, state)
}
