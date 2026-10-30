// Package systememail owns the instance's own mail sender: its settings and
// test messages. It is separate from the mailboxes members connect.
package systememail

import (
	"context"
	"errors"
	"strconv"
	"time"
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
	// ErrPasswordRequired also covers a new host, port, or username: the stored
	// password is only for the server and account it was entered for.
	ErrPasswordRequired = errors.New("a username needs a password")
)

// HTTP validation rejects these first; they guard other callers.
var (
	errSecurity = errors.New("unknown security mode")
	errPort     = errors.New("port is outside 1 to 65535")
)

// Input is the settings an administrator saves. An empty Password keeps the
// stored one if host, port, and username are unchanged; an empty Username
// removes both.
type Input struct {
	Host        string
	Port        int
	Security    Security
	Username    string
	Password    string
	FromAddress string
	FromName    string
}

// Settings is the saved configuration as shown to administrators. The
// password is write-only, so only whether one is stored is reported.
type Settings struct {
	Host        string
	Port        int
	Security    Security
	Username    string
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
// when the server needs no sign-in.
type Record struct {
	Host        string
	Port        int
	Security    Security
	Username    string
	Password    *Password
	FromAddress string
	FromName    string
	TestedAt    *time.Time
}

// Storage keeps the one configuration row.
type Storage interface {
	// ReadSystemEmail returns the saved record, nil when none is saved.
	ReadSystemEmail(ctx context.Context) (*Record, error)

	// UpdateSystemEmail serializes writers, loads the record (nil if absent),
	// calls update, and saves its result in one transaction. An error from
	// update rolls back and is returned unchanged.
	UpdateSystemEmail(ctx context.Context, update func(*Record) (Record, error)) error
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

// Save replaces the settings and clears their tested state, since a test
// of the old settings says nothing about the new ones.
func (s *Service) Save(ctx context.Context, input Input) (Settings, error) {
	if err := checkInput(input); err != nil {
		return Settings{}, err
	}

	// Encrypt before the write lock is taken.
	var sealed *Password
	if input.Password != "" {
		keyID, ciphertext, err := s.encryption.Seal([]byte(input.Password), passwordOwner(input.Host, input.Port, input.Username))
		if err != nil {
			return Settings{}, err
		}
		sealed = &Password{KeyID: keyID, Ciphertext: ciphertext}
	}

	var saved Record
	err := s.storage.UpdateSystemEmail(ctx, func(current *Record) (Record, error) {
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
			Host:        input.Host,
			Port:        input.Port,
			Security:    input.Security,
			Username:    input.Username,
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

// passwordOwner binds the encrypted password to its purpose and to the server
// and account it was entered for, so a ciphertext copied into other settings
// fails to decrypt. Changing the format makes stored passwords unreadable.
func passwordOwner(host string, port int, username string) string {
	// Quoting keeps the parts apart even when they contain the separator.
	return "system-email:" + strconv.Quote(host) + ":" + strconv.Itoa(port) + ":" + strconv.Quote(username)
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
	if input.Username == "" && input.Password != "" {
		return ErrPasswordWithoutUsername
	}
	return nil
}

func settingsOf(record Record) Settings {
	return Settings{
		Host:        record.Host,
		Port:        record.Port,
		Security:    record.Security,
		Username:    record.Username,
		HasPassword: record.Password != nil,
		FromAddress: record.FromAddress,
		FromName:    record.FromName,
		TestedAt:    record.TestedAt,
	}
}
