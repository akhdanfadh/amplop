// Package systememail owns the instance's own mail sender: its settings and
// test messages. It is separate from the mailboxes members connect.
package systememail

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Security selects how the connection to the mail server is protected.
type Security string

const (
	SecurityTLS      Security = "tls"      // TLS from the first byte, usually port 465.
	SecuritySTARTTLS Security = "starttls" // Required upgrade of a plain connection, usually port 587.
	SecurityNone     Security = "none"     // No protection: local development, never with credentials.
)

// Errors for settings that cannot be saved. None includes submitted values.
var (
	ErrCleartextUsername       = errors.New("a username needs TLS or STARTTLS")
	ErrPasswordWithoutUsername = errors.New("a password needs a username")
	// ErrPasswordRequired means a username has no password: none was typed,
	// and none is saved for the same host, port, and username.
	ErrPasswordRequired = errors.New("a username needs a password")

	ErrInvalidHost     = errors.New("host contains a control character")
	ErrInvalidUsername = errors.New("username contains a control character")
	ErrInvalidPassword = errors.New("password contains a control character or is longer than 255 bytes")
	ErrInvalidFromName = errors.New("from name contains a control character")
)

// maxPasswordBytes is the longest password every SASL PLAIN server accepts.
const maxPasswordBytes = 255

// HTTP validation rejects these first; they guard other callers.
var (
	errSecurity = errors.New("unknown security mode")
	errPort     = errors.New("port is outside 1 to 65535")
)

// Input is the settings an administrator saves. An empty Password keeps the
// stored one if host, port, and username are unchanged; an empty Username
// removes both.
type Input struct {
	Server
	Password    string
	FromAddress string
	FromName    string
}

// Settings is the saved configuration as shown to administrators. The
// password is write-only, so only whether one is stored is reported.
type Settings struct {
	Server
	HasPassword bool
	FromAddress string
	FromName    string
	TestedAt    *time.Time // nil: the current settings have not passed a test
}

// Password is an encrypted password and the ID of the key that sealed it.
type Password struct {
	KeyID      string
	Ciphertext []byte
}

// Record is the saved configuration. Username is empty, and Password nil,
// when the server needs no sign-in. Storage sets TestedAt and Revision: a
// save clears TestedAt and adds one to Revision, ignoring the Record's values.
type Record struct {
	Server
	Password    *Password
	FromAddress string
	FromName    string
	TestedAt    *time.Time
	Revision    int64
}

// Storage keeps the one configuration row.
type Storage interface {
	// ReadSystemEmail returns the saved record, nil when none is saved.
	ReadSystemEmail(ctx context.Context) (*Record, error)

	// UpdateSystemEmail serializes writers, loads the record (nil if absent),
	// calls update, saves its result, and records that actorID saved the
	// settings, all in one transaction. The event holds no setting values.
	// An error from update rolls back and is returned unchanged.
	UpdateSystemEmail(ctx context.Context, actorID int64, update func(*Record) (Record, error)) error
}

type sealer interface {
	Seal(plaintext []byte, owner string) (string, []byte, error)
}

// Service reads and saves the settings.
type Service struct {
	storage    Storage
	encryption sealer
}

// NewService supplies storage and the keys that encrypt the password.
func NewService(storage Storage, encryption sealer) *Service {
	return &Service{storage: storage, encryption: encryption}
}

// Settings returns the saved settings, nil when none are saved.
func (s *Service) Settings(ctx context.Context) (*Settings, error) {
	record, err := s.storage.ReadSystemEmail(ctx)
	if err != nil || record == nil {
		return nil, err
	}
	settings := settingsOf(*record)
	return &settings, nil
}

// Save replaces the settings, records actorID as the administrator who saved
// them, clears their tested time, and returns them. Every successful save is
// audited, even one that changes nothing.
func (s *Service) Save(ctx context.Context, actorID int64, input Input) (Settings, error) {
	if err := checkInput(input); err != nil {
		return Settings{}, err
	}

	// Encrypt before the write lock is taken.
	var sealed *Password
	if input.Password != "" {
		keyID, ciphertext, err := s.encryption.Seal([]byte(input.Password), passwordOwner(input.Server))
		if err != nil {
			return Settings{}, err
		}
		sealed = &Password{KeyID: keyID, Ciphertext: ciphertext}
	}

	var saved Record
	err := s.storage.UpdateSystemEmail(ctx, actorID, func(current *Record) (Record, error) {
		// Keeping the password for another server would let an administrator
		// send it there and read it, so a moved account needs it again.
		password := sealed
		if password == nil && input.Username != "" && current != nil &&
			current.Host == input.Host && current.Port == input.Port && current.Username == input.Username {
			password = current.Password
		}
		if input.Username != "" && password == nil {
			return Record{}, ErrPasswordRequired
		}
		saved = Record{
			Server:      input.Server,
			Password:    password,
			FromAddress: input.FromAddress,
			FromName:    input.FromName,
		}
		return saved, nil
	})
	if err != nil {
		return Settings{}, err
	}
	return settingsOf(saved), nil
}

// passwordOwner returns the owner that binds the encrypted password to system
// email and to server's host, port, and username.
//
// NOTE: A ciphertext copied into other settings fails to decrypt. Changing the
// format makes every stored password unreadable.
func passwordOwner(server Server) string {
	// Quoting keeps the parts apart even when they contain the separator.
	return "system-email:" + strconv.Quote(server.Host) + ":" + strconv.Itoa(server.Port) + ":" + strconv.Quote(server.Username)
}

// checkInput applies the rules that need no saved state.
func checkInput(input Input) error {
	if input.Port < 1 || input.Port > 65535 {
		return errPort
	}
	switch input.Security {
	case SecurityTLS, SecuritySTARTTLS:
	case SecurityNone:
		if input.Username != "" {
			return ErrCleartextUsername
		}
	default:
		return errSecurity
	}
	// SMTP commands end at CRLF and headers cannot hold control characters.
	if strings.ContainsFunc(input.Host, unicode.IsControl) {
		return ErrInvalidHost
	}
	if strings.ContainsFunc(input.Username, unicode.IsControl) {
		return ErrInvalidUsername
	}
	// SASL PLAIN separates its fields with NUL and guarantees only 255 octets
	// (RFC 4616).
	if strings.ContainsFunc(input.Password, unicode.IsControl) || len(input.Password) > maxPasswordBytes {
		return ErrInvalidPassword
	}
	if strings.ContainsFunc(input.FromName, unicode.IsControl) {
		return ErrInvalidFromName
	}
	if input.Username == "" && input.Password != "" {
		return ErrPasswordWithoutUsername
	}
	return nil
}

func settingsOf(record Record) Settings {
	return Settings{
		Server:      record.Server,
		HasPassword: record.Password != nil,
		FromAddress: record.FromAddress,
		FromName:    record.FromName,
		TestedAt:    record.TestedAt,
	}
}
