package systememail

import (
	"context"
	"errors"
	"time"
)

const (
	testMessagePurpose = "test message"
	testMessageSubject = "Test message from amplop"
	testMessageBody    = "This is a test message from amplop. An administrator sent it from\n" +
		"amplop's settings.\n\n" +
		"If you can read this, the system email settings work. You don't need to reply.\n"
)

// testMessageStorage records the settings' tested time.
type testMessageStorage interface {
	// UpdateSystemEmailTestedAt saves testedAt as the tested time, without an
	// audit event, if the settings still have revision; otherwise it changes
	// nothing.
	UpdateSystemEmailTestedAt(ctx context.Context, revision int64, testedAt *time.Time) error
}

// TestMessages sends test messages with the saved settings.
type TestMessages struct {
	storage testMessageStorage
	mailer  *Mailer
	now     func() time.Time
}

// NewTestMessages supplies storage, the mailer that sends with the saved
// settings, and a clock for the tested time.
func NewTestMessages(storage testMessageStorage, mailer *Mailer, now func() time.Time) *TestMessages {
	return &TestMessages{storage: storage, mailer: mailer, now: now}
}

// TestMessageResult is whether the server accepted the test message. Reason
// names the failed step when it did not.
type TestMessageResult struct {
	Accepted bool
	Reason   Reason
}

// Send sends a test message to recipient with the saved settings and records
// whether they passed. It returns [emailaddress.ErrInvalid] for an invalid
// recipient and [ErrNotConfigured] when nothing is saved, both without
// changing the tested time, and [ErrPasswordUnreadable], after clearing the
// tested time of the settings it read, when the password cannot be
// decrypted. It returns other errors when the settings cannot be read, the
// send fails for a reason other than the server or network, or the result
// cannot be recorded.
func (t *TestMessages) Send(ctx context.Context, recipient string) (TestMessageResult, error) {
	revision, err := t.mailer.Send(ctx, Outgoing{
		Purpose: testMessagePurpose,
		To:      recipient,
		Subject: testMessageSubject,
		Body:    testMessageBody,
	})
	result := TestMessageResult{Accepted: true}
	if failed, ok := errors.AsType[*SendError](err); ok {
		result = TestMessageResult{Reason: failed.Reason}
	} else if errors.Is(err, ErrPasswordUnreadable) {
		// Settings that cannot be decrypted cannot send, so they have not passed.
		return TestMessageResult{}, errors.Join(err, t.storage.UpdateSystemEmailTestedAt(ctx, revision, nil))
	} else if err != nil {
		return TestMessageResult{}, err
	}

	// A pass sets the tested time and a failure clears it. A save during the
	// send changed the revision, so the saved settings keep their own.
	var testedAt *time.Time
	if result.Accepted {
		now := t.now()
		testedAt = &now
	}
	if err := t.storage.UpdateSystemEmailTestedAt(ctx, revision, testedAt); err != nil {
		return TestMessageResult{}, err
	}
	return result, nil
}
