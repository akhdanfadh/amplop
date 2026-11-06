package systememail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"inbok/internal/emailaddress"
)

// maxLoggedReply is how much of a failed send's error is logged.
const maxLoggedReply = 512

var (
	// ErrNotConfigured means no settings are saved, so nothing can be sent.
	ErrNotConfigured = errors.New("system email is not configured")
	// ErrNotTested means the saved settings have not passed a test message,
	// and the message asked for tested settings with [Outgoing.RequireTested].
	ErrNotTested = errors.New("system email has not passed a test")
	// ErrPasswordUnreadable means the saved password cannot be decrypted, e.g.
	// after its key was removed.
	ErrPasswordUnreadable = errors.New("the saved system email password cannot be decrypted")
)

// mailerStorage reads the saved settings.
type mailerStorage interface {
	// ReadSystemEmail returns the saved record, nil when none is saved.
	ReadSystemEmail(ctx context.Context) (*Record, error)
}

type opener interface {
	Open(id string, ciphertext []byte, owner string) ([]byte, error)
}

// Mailer sends inbok's own messages with the saved settings.
type Mailer struct {
	storage    mailerStorage
	encryption opener
	sender     Sender
	logger     *slog.Logger
}

// NewMailer supplies storage, the keys that decrypt the password, the sender,
// and a logger for failed sends.
func NewMailer(storage mailerStorage, encryption opener, sender Sender, logger *slog.Logger) *Mailer {
	return &Mailer{storage: storage, encryption: encryption, sender: sender, logger: logger}
}

// Outgoing is one plain-text message for [Mailer.Send]. Its From address
// and name come from the saved settings.
type Outgoing struct {
	Purpose string // names the message in logs, e.g. "test message"
	To      string
	Subject string
	Body    string

	// Private keeps the server's reply out of a failed send's log, since a
	// reply can quote the message. Set it exactly when [Outgoing.Body] holds
	// a secret, something that grants access if read, e.g. a link's token; a
	// reply that quotes only the recipient's address is no reason to.
	Private bool

	// RequireTested sends only with settings that passed a test message,
	// judged on the settings the send would use. Set it for mail nobody
	// asked for, e.g. the recovery notice to other administrators, so inbok
	// does not push it, and retry it, through settings no one has seen
	// work. Mail someone asked for or caused, e.g. a link or the notice of
	// their own reset, goes out whenever settings are configured, so a
	// saved but untested change does not stop it.
	RequireTested bool
}

// Send sends outgoing with the saved settings, logs a failed send, without the
// server's reply when outgoing is [Outgoing.Private], and returns the
// settings' revision. It returns [emailaddress.ErrInvalid] for an invalid
// [Outgoing.To] before reading the settings, [ErrNotConfigured] when nothing
// is saved, [ErrNotTested] when [Outgoing.RequireTested] is set and the
// settings have not passed a test, [ErrPasswordUnreadable] when the
// password cannot be decrypted, a [*SendError] when the server or network
// fails, and the [Sender]'s other errors unchanged. Every error after the
// settings are read comes with their revision.
func (m *Mailer) Send(ctx context.Context, outgoing Outgoing) (int64, error) {
	// 1. Refuse an invalid recipient before reading the settings.
	to, err := emailaddress.Parse(outgoing.To)
	if err != nil {
		return 0, err
	}

	// 2. Read the settings, and refuse missing or untested ones before
	// decrypting anything.
	saved, err := m.storage.ReadSystemEmail(ctx)
	if err != nil {
		return 0, err
	}
	if saved == nil {
		return 0, ErrNotConfigured
	}
	if outgoing.RequireTested && saved.TestedAt == nil {
		return saved.Revision, ErrNotTested
	}

	// 3. Decrypt the password.
	var password string
	if saved.Password != nil {
		plaintext, err := m.encryption.Open(saved.Password.KeyID, saved.Password.Ciphertext, passwordOwner(saved.Server))
		if err != nil {
			return saved.Revision, fmt.Errorf("%w: %w", ErrPasswordUnreadable, err)
		}
		password = string(plaintext)
	}

	// 4. Send, and log a failure.
	message := Message{
		FromAddress: saved.FromAddress,
		FromName:    saved.FromName,
		To:          to,
		Subject:     outgoing.Subject,
		Body:        outgoing.Body,
	}
	err = m.sender.Send(ctx, saved.Server, password, message)
	if failed, ok := errors.AsType[*SendError](err); ok {
		attrs := []any{"purpose", outgoing.Purpose, "reason", failed.Reason,
			"host", saved.Host, "port", saved.Port, "security", saved.Security, "username", saved.Username}
		if failed.Unconfirmed {
			attrs = append(attrs, "unconfirmed", true)
		}
		// The reply can quote the message, in any encoding or cut anywhere, so
		// no part of a private one is safe to keep.
		if !outgoing.Private {
			// The sender hides the credentials and the recipient's address in
			// the reply, which can still name the username, and can be long,
			// so only its start is kept.
			attrs = append(attrs, "err", truncate(failed.Err.Error(), maxLoggedReply))
		}
		m.logger.WarnContext(ctx, "system email send failed", attrs...)
	}
	return saved.Revision, err
}

// truncate returns s cut to at most n bytes, keeping valid UTF-8, followed by
// "…" when it was cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}
