package systememail

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryStorage struct {
	record *Record
	actors []int64 // one per committed save
	err    error
}

func (m *memoryStorage) ReadSystemEmail(context.Context) (*Record, error) {
	return m.record, m.err
}

func (m *memoryStorage) UpdateSystemEmail(_ context.Context, actorID int64, update func(*Record) (Record, error)) error {
	if m.err != nil {
		return m.err
	}
	next, err := update(m.record)
	if err != nil {
		return err
	}
	// Like the table: a save clears the tested time and is a new revision.
	next.TestedAt, next.Revision = nil, 1
	if m.record != nil {
		next.Revision = m.record.Revision + 1
	}
	m.record = &next
	m.actors = append(m.actors, actorID)
	return nil
}

func (m *memoryStorage) UpdateSystemEmailTestedAt(_ context.Context, revision int64, testedAt *time.Time) error {
	if m.err != nil {
		return m.err
	}
	if m.record == nil || m.record.Revision != revision {
		return nil
	}
	next := *m.record
	next.TestedAt = testedAt
	m.record = &next
	return nil
}

// adminID is the administrator saving settings in these tests.
const adminID int64 = 7

// signInOwner is the password owner for inputWithSignIn's server and account.
const signInOwner = `system-email:"smtp.example.com":587:"inbok@example.com"`

// fakeKeyring seals by prefixing "sealed:" and opens only for the sealing owner.
type fakeKeyring struct {
	owner     string
	plaintext []byte
	err       error
}

func (f *fakeKeyring) Seal(plaintext []byte, owner string) (string, []byte, error) {
	f.owner, f.plaintext = owner, plaintext
	return "1", []byte("sealed:" + owner + ":" + string(plaintext)), f.err
}

func (f *fakeKeyring) Open(id string, ciphertext []byte, owner string) ([]byte, error) {
	plaintext, ok := strings.CutPrefix(string(ciphertext), "sealed:"+owner+":")
	if f.err != nil || id != "1" || !ok {
		return nil, errors.Join(f.err, errors.New("cannot open"))
	}
	return []byte(plaintext), nil
}

// inputWithSignIn is a typical provider's settings with a username and password.
func inputWithSignIn() Input {
	return Input{
		Server: Server{
			Host:     "smtp.example.com",
			Port:     587,
			Security: SecuritySTARTTLS,
			Username: "inbok@example.com",
		},
		Password:    "app password",
		FromAddress: "inbok@example.com",
		FromName:    "inbok",
	}
}

// storedSignIn is what saving inputWithSignIn stores, tested at a fixed time.
func storedSignIn() *Record {
	tested := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	return &Record{
		Server:      Server{Host: "smtp.example.com", Port: 587, Security: SecuritySTARTTLS, Username: "inbok@example.com"},
		Password:    &Password{KeyID: "1", Ciphertext: []byte("stored")},
		FromAddress: "inbok@example.com", FromName: "inbok", TestedAt: &tested, Revision: 3,
	}
}

func TestSystemEmailNotConfigured(t *testing.T) {
	service := NewService(&memoryStorage{}, &fakeKeyring{})

	settings, err := service.Settings(t.Context())
	require.NoError(t, err)

	assert.Nil(t, settings)
}

func TestSystemEmailSave(t *testing.T) {
	storage, sealer := &memoryStorage{}, &fakeKeyring{}
	service := NewService(storage, sealer)

	saved, err := service.Save(t.Context(), adminID, inputWithSignIn())
	require.NoError(t, err)

	assert.Equal(t, Settings{
		Server:      Server{Host: "smtp.example.com", Port: 587, Security: SecuritySTARTTLS, Username: "inbok@example.com"},
		HasPassword: true,
		FromAddress: "inbok@example.com", FromName: "inbok",
	}, saved)
	assert.Equal(t, signInOwner, sealer.owner)
	assert.Equal(t, []byte("app password"), sealer.plaintext)
	require.NotNil(t, storage.record)
	assert.Equal(t, &Password{KeyID: "1", Ciphertext: []byte("sealed:" + signInOwner + ":app password")}, storage.record.Password)
	assert.Equal(t, []int64{adminID}, storage.actors, "the save names its administrator")
	read, err := service.Settings(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &saved, read)
}

// The owner keeps its parts apart, so moving text between them changes it.
func TestSystemEmailPasswordOwner(t *testing.T) {
	assert.NotEqual(t, passwordOwner(Server{Host: "a:1", Port: 2, Username: "b"}), passwordOwner(Server{Host: "a", Port: 1, Username: `2:"b`}))
	assert.NotEqual(t, passwordOwner(Server{Host: "smtp.example.com", Port: 587, Username: "a"}), passwordOwner(Server{Host: "smtp.example.com", Port: 465, Username: "a"}))
}

func TestSystemEmailSaveWithoutSignIn(t *testing.T) {
	for _, security := range []Security{SecurityTLS, SecuritySTARTTLS, SecurityNone} {
		t.Run(string(security), func(t *testing.T) {
			storage := &memoryStorage{record: storedSignIn()}
			input := Input{Server: Server{Host: "smtp.example.com", Port: 587, Security: security}, FromAddress: "inbok@example.com", FromName: "inbok"}

			saved, err := NewService(storage, &fakeKeyring{}).Save(t.Context(), adminID, input)
			require.NoError(t, err)

			assert.False(t, saved.HasPassword)
			assert.Empty(t, saved.Username)
			require.NotNil(t, storage.record)
			assert.Empty(t, storage.record.Username)
			assert.Nil(t, storage.record.Password, "removing the username removes the password")
		})
	}
}

func TestSystemEmailSaveKeepsPassword(t *testing.T) {
	stored := storedSignIn()
	storage, sealer := &memoryStorage{record: stored}, &fakeKeyring{}
	input := inputWithSignIn()
	input.Password, input.Security, input.FromName = "", SecurityTLS, "Mail"

	saved, err := NewService(storage, sealer).Save(t.Context(), adminID, input)
	require.NoError(t, err)

	assert.True(t, saved.HasPassword)
	assert.Nil(t, sealer.plaintext, "an empty password is not encrypted")
	require.NotNil(t, storage.record)
	assert.Same(t, stored.Password, storage.record.Password)
	assert.Equal(t, SecurityTLS, storage.record.Security)
}

func TestSystemEmailSaveReplacesPassword(t *testing.T) {
	storage := &memoryStorage{record: storedSignIn()}

	_, err := NewService(storage, &fakeKeyring{}).Save(t.Context(), adminID, inputWithSignIn())
	require.NoError(t, err)

	require.NotNil(t, storage.record)
	assert.Equal(t, &Password{KeyID: "1", Ciphertext: []byte("sealed:" + signInOwner + ":app password")}, storage.record.Password)
}

func TestSystemEmailSaveClearsTested(t *testing.T) {
	storage := &memoryStorage{record: storedSignIn()}
	input := inputWithSignIn()
	input.Password = ""

	saved, err := NewService(storage, &fakeKeyring{}).Save(t.Context(), adminID, input)
	require.NoError(t, err)

	assert.Nil(t, saved.TestedAt)
	require.NotNil(t, storage.record)
	assert.Nil(t, storage.record.TestedAt, "even unchanged settings need a new test")
}

// A password of exactly 255 bytes fits SASL PLAIN, whatever its characters.
func TestSystemEmailSaveLongestPassword(t *testing.T) {
	storage, sealer := &memoryStorage{}, &fakeKeyring{}
	input := inputWithSignIn()
	input.Password = strings.Repeat("é", 127) + "a" // 255 bytes

	_, err := NewService(storage, sealer).Save(t.Context(), adminID, input)
	require.NoError(t, err)

	assert.Equal(t, []byte(input.Password), sealer.plaintext)
}

func TestSystemEmailSaveRejected(t *testing.T) {
	cleartext := inputWithSignIn()
	cleartext.Security = SecurityNone
	orphan := inputWithSignIn()
	orphan.Username = ""
	unknown := inputWithSignIn()
	unknown.Security = "ssl"
	port := inputWithSignIn()
	port.Port = 65536
	host := inputWithSignIn()
	host.Host = "smtp.example.com\r\nRCPT TO:<x@example.net>"
	username := inputWithSignIn()
	username.Username = "inbok\x00admin"
	nulPassword := inputWithSignIn()
	nulPassword.Password = "app\x00password"
	longPassword := inputWithSignIn()
	longPassword.Password = strings.Repeat("é", 128) // 128 characters, 256 bytes
	fromName := inputWithSignIn()
	fromName.FromName = "inbok\u0085"
	for _, c := range []struct {
		name  string
		input Input
		want  error
	}{
		{"username without TLS", cleartext, ErrCleartextUsername},
		{"password without username", orphan, ErrPasswordWithoutUsername},
		{"unknown security", unknown, errSecurity},
		{"port out of range", port, errPort},
		{"control in host", host, ErrInvalidHost},
		{"control in username", username, ErrInvalidUsername},
		{"control in password", nulPassword, ErrInvalidPassword},
		{"password over 255 bytes", longPassword, ErrInvalidPassword},
		{"control in from name", fromName, ErrInvalidFromName},
	} {
		t.Run(c.name, func(t *testing.T) {
			stored := storedSignIn()
			storage, sealer := &memoryStorage{record: stored}, &fakeKeyring{}

			saved, err := NewService(storage, sealer).Save(t.Context(), adminID, c.input)
			require.ErrorIs(t, err, c.want)

			assert.Empty(t, saved)
			assert.Same(t, stored, storage.record)
			assert.Empty(t, storage.actors, "a rejected save is not audited")
			assert.Nil(t, sealer.plaintext, "rejected settings are not encrypted")
		})
	}
}

// A username needs a password entered for this host, port, and username.
func TestSystemEmailSavePasswordRequired(t *testing.T) {
	withoutSignIn := storedSignIn()
	withoutSignIn.Username, withoutSignIn.Password = "", nil
	for _, c := range []struct {
		name   string
		stored *Record
		change func(*Input)
	}{
		{"first save", nil, func(*Input) {}},
		{"stored without password", withoutSignIn, func(*Input) {}},
		{"new host", storedSignIn(), func(in *Input) { in.Host = "smtp.attacker.example" }},
		{"new port", storedSignIn(), func(in *Input) { in.Port = 465 }},
		{"new username", storedSignIn(), func(in *Input) { in.Username = "other@example.com" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage := &memoryStorage{record: c.stored}
			input := inputWithSignIn()
			input.Password = ""
			c.change(&input)

			saved, err := NewService(storage, &fakeKeyring{}).Save(t.Context(), adminID, input)
			require.ErrorIs(t, err, ErrPasswordRequired)

			assert.Empty(t, saved)
			assert.Same(t, c.stored, storage.record)
			assert.Empty(t, storage.actors, "a rejected save is not audited")
		})
	}
}

func TestSystemEmailSaveFailure(t *testing.T) {
	failure := errors.New("storage or encryption unavailable")
	for _, c := range []struct {
		name    string
		storage *memoryStorage
		sealer  *fakeKeyring
	}{
		{"storage", &memoryStorage{err: failure}, &fakeKeyring{}},
		{"encryption", &memoryStorage{}, &fakeKeyring{err: failure}},
	} {
		t.Run(c.name, func(t *testing.T) {
			saved, err := NewService(c.storage, c.sealer).Save(t.Context(), adminID, inputWithSignIn())
			require.ErrorIs(t, err, failure)

			assert.Empty(t, saved)
			assert.Nil(t, c.storage.record)
		})
	}
}

func TestSystemEmailSettingsFailure(t *testing.T) {
	failure := errors.New("storage unavailable")

	settings, err := NewService(&memoryStorage{err: failure}, &fakeKeyring{}).Settings(t.Context())
	require.ErrorIs(t, err, failure)

	assert.Nil(t, settings)
}
