package systememail

import (
	"context"
	"errors"
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
	m.record = &next
	m.actors = append(m.actors, actorID)
	return nil
}

// adminID is the administrator saving settings in these tests.
const adminID int64 = 7

type fakeSealer struct {
	owner     string
	plaintext []byte
	err       error
}

func (f *fakeSealer) Seal(plaintext []byte, owner string) (string, []byte, error) {
	f.owner, f.plaintext = owner, plaintext
	return "1", []byte("sealed:" + string(plaintext)), f.err
}

// inputWithSignIn is a typical provider's settings with a username and password.
func inputWithSignIn() Input {
	return Input{
		Host:        "smtp.example.com",
		Port:        587,
		Security:    SecuritySTARTTLS,
		Username:    "inbok@example.com",
		Password:    "app password",
		FromAddress: "inbok@example.com",
		FromName:    "inbok",
	}
}

// storedSignIn is what saving inputWithSignIn stores, tested at a fixed time.
func storedSignIn() *Record {
	tested := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	return &Record{
		Host: "smtp.example.com", Port: 587, Security: SecuritySTARTTLS,
		Username: "inbok@example.com", Password: &Password{KeyID: "1", Ciphertext: []byte("stored")},
		FromAddress: "inbok@example.com", FromName: "inbok", TestedAt: &tested,
	}
}

func TestSystemEmailNotConfigured(t *testing.T) {
	service := NewService(&memoryStorage{}, &fakeSealer{})

	settings, err := service.Settings(t.Context())
	require.NoError(t, err)

	assert.Nil(t, settings)
}

func TestSystemEmailSave(t *testing.T) {
	storage, sealer := &memoryStorage{}, &fakeSealer{}
	service := NewService(storage, sealer)

	saved, err := service.Save(t.Context(), adminID, inputWithSignIn())
	require.NoError(t, err)

	assert.Equal(t, Settings{
		Host: "smtp.example.com", Port: 587, Security: SecuritySTARTTLS,
		Username: "inbok@example.com", HasPassword: true,
		FromAddress: "inbok@example.com", FromName: "inbok",
	}, saved)
	assert.Equal(t, `system-email:"smtp.example.com":587:"inbok@example.com"`, sealer.owner)
	assert.Equal(t, []byte("app password"), sealer.plaintext)
	require.NotNil(t, storage.record)
	assert.Equal(t, &Password{KeyID: "1", Ciphertext: []byte("sealed:app password")}, storage.record.Password)
	assert.Equal(t, []int64{adminID}, storage.actors, "the save names its administrator")
	read, err := service.Settings(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &saved, read)
}

// The owner keeps its parts apart, so moving text between them changes it.
func TestSystemEmailPasswordOwner(t *testing.T) {
	assert.NotEqual(t, passwordOwner("a:1", 2, "b"), passwordOwner("a", 1, `2:"b`))
	assert.NotEqual(t, passwordOwner("smtp.example.com", 587, "a"), passwordOwner("smtp.example.com", 465, "a"))
}

func TestSystemEmailSaveWithoutSignIn(t *testing.T) {
	for _, security := range []Security{SecurityTLS, SecuritySTARTTLS, SecurityNone} {
		t.Run(string(security), func(t *testing.T) {
			storage := &memoryStorage{record: storedSignIn()}
			input := Input{Host: "smtp.example.com", Port: 587, Security: security, FromAddress: "inbok@example.com", FromName: "inbok"}

			saved, err := NewService(storage, &fakeSealer{}).Save(t.Context(), adminID, input)
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
	storage, sealer := &memoryStorage{record: stored}, &fakeSealer{}
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

	_, err := NewService(storage, &fakeSealer{}).Save(t.Context(), adminID, inputWithSignIn())
	require.NoError(t, err)

	require.NotNil(t, storage.record)
	assert.Equal(t, &Password{KeyID: "1", Ciphertext: []byte("sealed:app password")}, storage.record.Password)
}

func TestSystemEmailSaveClearsTested(t *testing.T) {
	storage := &memoryStorage{record: storedSignIn()}
	input := inputWithSignIn()
	input.Password = ""

	saved, err := NewService(storage, &fakeSealer{}).Save(t.Context(), adminID, input)
	require.NoError(t, err)

	assert.Nil(t, saved.TestedAt)
	require.NotNil(t, storage.record)
	assert.Nil(t, storage.record.TestedAt, "even unchanged settings need a new test")
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
	for _, c := range []struct {
		name  string
		input Input
		want  error
	}{
		{"username without TLS", cleartext, ErrCleartextUsername},
		{"password without username", orphan, ErrPasswordWithoutUsername},
		{"unknown security", unknown, errSecurity},
		{"port out of range", port, errPort},
	} {
		t.Run(c.name, func(t *testing.T) {
			stored := storedSignIn()
			storage, sealer := &memoryStorage{record: stored}, &fakeSealer{}

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

			saved, err := NewService(storage, &fakeSealer{}).Save(t.Context(), adminID, input)
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
		sealer  *fakeSealer
	}{
		{"storage", &memoryStorage{err: failure}, &fakeSealer{}},
		{"encryption", &memoryStorage{}, &fakeSealer{err: failure}},
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

	settings, err := NewService(&memoryStorage{err: failure}, &fakeSealer{}).Settings(t.Context())
	require.ErrorIs(t, err, failure)

	assert.Nil(t, settings)
}
