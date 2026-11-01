package systememail

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

const (
	testMessageSubject = "Test message from inbok"
	testMessageBody    = "This is a test message from inbok. An administrator sent it from\n" +
		"inbok's settings.\n\n" +
		"If you can read this, the system email settings work. You don't need to reply.\n"
	// maxLoggedReply is how much of a failed send's error is logged.
	maxLoggedReply = 512
)

// ErrNotConfigured means no settings are saved, so there is nothing to test.
var ErrNotConfigured = errors.New("system email is not configured")

// testMessageStorage reads the settings and records their tested time.
type testMessageStorage interface {
	// ReadSystemEmail returns the saved record, nil when none is saved.
	ReadSystemEmail(ctx context.Context) (*Record, error)

	// UpdateSystemEmailTestedAt saves testedAt as the tested time, without an
	// audit event, if the settings still have revision; otherwise it changes
	// nothing.
	UpdateSystemEmailTestedAt(ctx context.Context, revision int64, testedAt *time.Time) error
}

type opener interface {
	Open(id string, ciphertext []byte, owner string) ([]byte, error)
}

// TestMessages sends test messages with the saved settings.
type TestMessages struct {
	storage    testMessageStorage
	encryption opener
	sender     Sender
	now        func() time.Time
	logger     *slog.Logger
}

// NewTestMessages supplies storage, the keys that decrypt the password, the
// sender, a clock for the tested time, and a logger for failed sends.
func NewTestMessages(storage testMessageStorage, encryption opener, sender Sender, now func() time.Time, logger *slog.Logger) *TestMessages {
	return &TestMessages{storage: storage, encryption: encryption, sender: sender, now: now, logger: logger}
}

// TestMessageResult is whether the server accepted the test message. Reason
// names the failed step when it did not.
type TestMessageResult struct {
	Accepted bool
	Reason   Reason
}

// Send sends a test message to recipient, an email address already checked
// by request validation, with the saved settings and records whether they
// passed. It returns ErrNotConfigured when nothing is saved, and an error when
// the password cannot be decrypted, the send fails for a reason other than
// the server or network, or the result cannot be recorded.
func (t *TestMessages) Send(ctx context.Context, recipient string) (TestMessageResult, error) {
	saved, err := t.storage.ReadSystemEmail(ctx)
	if err != nil {
		return TestMessageResult{}, err
	}
	if saved == nil {
		return TestMessageResult{}, ErrNotConfigured
	}
	var password string
	if saved.Password != nil {
		plaintext, err := t.encryption.Open(saved.Password.KeyID, saved.Password.Ciphertext, passwordOwner(saved.Server))
		if err != nil {
			// Settings that cannot be decrypted cannot send, so they have not passed.
			return TestMessageResult{}, errors.Join(err, t.storage.UpdateSystemEmailTestedAt(ctx, saved.Revision, nil))
		}
		password = string(plaintext)
	}

	message := Message{
		FromAddress: saved.FromAddress,
		FromName:    saved.FromName,
		To:          recipient,
		Subject:     testMessageSubject,
		Body:        testMessageBody,
	}
	result := TestMessageResult{Accepted: true}
	err = t.sender.Send(ctx, saved.Server, password, message)
	if failed, ok := errors.AsType[*SendError](err); ok {
		result = TestMessageResult{Reason: failed.Reason}
		// The server's reply can name the username, never the password, and can
		// be long, so only its start is kept.
		t.logger.WarnContext(ctx, "system email test message failed", "reason", failed.Reason,
			"host", saved.Host, "port", saved.Port, "security", saved.Security, "username", saved.Username,
			"err", truncate(failed.Err.Error(), maxLoggedReply))
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
	if err := t.storage.UpdateSystemEmailTestedAt(ctx, saved.Revision, testedAt); err != nil {
		return TestMessageResult{}, err
	}
	return result, nil
}

// truncate returns s cut to at most n bytes, keeping valid UTF-8, followed by
// "…" when it was cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}
