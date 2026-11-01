package systememail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// maxLoggedReply is how much of a failed send's error is logged.
const maxLoggedReply = 512

var (
	// ErrNotConfigured means no settings are saved, so nothing can be sent.
	ErrNotConfigured = errors.New("system email is not configured")
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
}

// Send sends outgoing with the saved settings, logs a failed send, and
// returns the settings' revision. It returns ErrNotConfigured when nothing is
// saved, ErrPasswordUnreadable when the password cannot be decrypted, a
// [*SendError] when the server or network fails, and the [Sender]'s other
// errors unchanged. Every error after the settings are read comes with their
// revision.
func (m *Mailer) Send(ctx context.Context, outgoing Outgoing) (int64, error) {
	saved, err := m.storage.ReadSystemEmail(ctx)
	if err != nil {
		return 0, err
	}
	if saved == nil {
		return 0, ErrNotConfigured
	}
	var password string
	if saved.Password != nil {
		plaintext, err := m.encryption.Open(saved.Password.KeyID, saved.Password.Ciphertext, passwordOwner(saved.Server))
		if err != nil {
			return saved.Revision, fmt.Errorf("%w: %w", ErrPasswordUnreadable, err)
		}
		password = string(plaintext)
	}

	message := Message{
		FromAddress: saved.FromAddress,
		FromName:    saved.FromName,
		To:          outgoing.To,
		Subject:     outgoing.Subject,
		Body:        outgoing.Body,
	}
	err = m.sender.Send(ctx, saved.Server, password, message)
	if failed, ok := errors.AsType[*SendError](err); ok {
		// The server's reply can name the username, never the password, and can
		// be long, so only its start is kept.
		m.logger.WarnContext(ctx, "system email send failed", "purpose", outgoing.Purpose, "reason", failed.Reason,
			"host", saved.Host, "port", saved.Port, "security", saved.Security, "username", saved.Username,
			"err", truncate(failed.Err.Error(), maxLoggedReply))
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
