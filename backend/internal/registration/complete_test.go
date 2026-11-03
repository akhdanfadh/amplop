package registration

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/passwordhash"
	"inbok/internal/randomsecret"
)

const testPassword = "correct horse battery"

// completeMemory keeps links and accounts like the database does.
type completeMemory struct {
	registration Record
	links        []Link
	accounts     []string // emails that have an account
	members      []CompleteChange
	readErr      error
	locked       int // calls to CompleteRegistration

	// beforeLock runs before CompleteRegistration reads, like a write that
	// commits while the password is hashed.
	beforeLock func(*completeMemory)
}

// state returns the CompleteState for the link with hash.
func (m *completeMemory) state(hash [sha256.Size]byte) CompleteState {
	state := CompleteState{Registration: m.registration}
	at := slices.IndexFunc(m.links, func(l Link) bool { return l.TokenHash == hash })
	if at < 0 {
		return state
	}
	link := m.links[at]
	state.Link = &link
	state.AccountExists = slices.Contains(m.accounts, link.Email)
	return state
}

func (m *completeMemory) ReadCompletion(_ context.Context, hash [sha256.Size]byte) (CompleteState, error) {
	return m.state(hash), m.readErr
}

func (m *completeMemory) CompleteRegistration(_ context.Context, hash [sha256.Size]byte, email string, create func(CompleteState) (CompleteChange, error)) (account.User, error) {
	m.locked++
	if m.beforeLock != nil {
		m.beforeLock(m)
	}
	state := m.state(hash)
	state.AccountExists = slices.Contains(m.accounts, email)
	change, err := create(state)
	if err != nil {
		return account.User{}, err
	}
	email = state.Link.Email
	m.members = append(m.members, change)
	m.accounts = append(m.accounts, email)
	m.links = slices.DeleteFunc(m.links, func(l Link) bool { return l.Email == email })
	return account.User{ID: int64(len(m.members)), Email: email, Role: account.RoleMember}, nil
}

// completeSetup returns open registration with one live link to visitor,
// and the link's secret.
func completeSetup(t *testing.T) (*completeMemory, string) {
	t.Helper()

	secret, hash := randomsecret.New()
	return &completeMemory{
		registration: Record{State: StateOpen, SystemEmailTested: true},
		links:        []Link{{TokenHash: hash, Email: visitor, CreatedAt: testNow, ExpiresAt: testNow.Add(linkLifetime)}},
	}, secret
}

func testCompletion(storage *completeMemory) *Completion {
	return NewCompletion(storage, func() time.Time { return testNow })
}

func TestCompleteCreatesMember(t *testing.T) {
	storage, secret := completeSetup(t)
	_, otherHash := randomsecret.New()
	storage.links = append(storage.links, Link{TokenHash: otherHash, Email: otherEmail, ExpiresAt: testNow.Add(linkLifetime)})

	user, err := testCompletion(storage).Complete(t.Context(), secret, testPassword)
	require.NoError(t, err)

	assert.Equal(t, account.User{ID: 1, Email: visitor, Role: account.RoleMember}, user)
	require.Len(t, storage.members, 1)
	member := storage.members[0]
	// Following the link proved the email.
	assert.Equal(t, testNow, member.VerifiedAt)
	ok, err := passwordhash.Verify(t.Context(), testPassword, member.PasswordHash)
	require.NoError(t, err)
	assert.True(t, ok, "the hash is of the password")
	// The used link is gone; another email's link stays.
	assert.Equal(t, []string{otherEmail}, linkEmails(storage.links))
}

// linkEmails returns each link's email.
func linkEmails(links []Link) []string {
	var emails []string
	for _, link := range links {
		emails = append(emails, link.Email)
	}
	return emails
}

func TestCompleteRefuses(t *testing.T) {
	cases := []struct {
		name     string
		password string
		secret   func(string) string
		change   func(*completeMemory)
		want     error
	}{
		{name: "short password", password: strings.Repeat("a", account.PasswordMinLength-1), want: account.ErrPasswordInvalid},
		{name: "long password", password: strings.Repeat("a", account.PasswordMaxLength+1), want: account.ErrPasswordInvalid},
		{name: "password not UTF-8", password: testPassword + "\xff", want: account.ErrPasswordInvalid},
		{name: "malformed secret", secret: func(string) string { return "not-a-secret" }, want: ErrLinkInvalid},
		{name: "unknown secret", secret: func(string) string { secret, _ := randomsecret.New(); return secret }, want: ErrLinkInvalid},
		{name: "expired link", change: func(m *completeMemory) { m.links[0].ExpiresAt = testNow }, want: ErrLinkInvalid},
		{name: "closed", change: func(m *completeMemory) { m.registration.State = StateClosed }, want: ErrClosed},
		{name: "paused", change: func(m *completeMemory) { m.registration.SystemEmailTested = false }, want: ErrClosed},
		// Closing deleted the link: the visitor learns why it stopped working.
		{name: "closed without the link", change: func(m *completeMemory) { m.registration.State = StateClosed; m.links = nil }, want: ErrClosed},
		{name: "account exists", change: func(m *completeMemory) { m.accounts = []string{visitor} }, want: ErrAccountExists},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage, secret := completeSetup(t)
			if c.secret != nil {
				secret = c.secret(secret)
			}
			if c.change != nil {
				c.change(storage)
			}
			password := testPassword
			if c.password != "" {
				password = c.password
			}

			_, err := testCompletion(storage).Complete(t.Context(), secret, password)

			require.ErrorIs(t, err, c.want)
			// Refused before the hash, so guesses cost no Argon2id work.
			assert.Zero(t, storage.locked)
			assert.Empty(t, storage.members)
		})
	}
}

// The locked read decides: a change while the password was hashed wins.
func TestCompleteRechecksUnderLock(t *testing.T) {
	cases := []struct {
		name   string
		change func(*completeMemory)
		want   error
	}{
		{name: "closed", change: func(m *completeMemory) { m.registration.State = StateClosed; m.links = nil }, want: ErrClosed},
		{name: "paused", change: func(m *completeMemory) { m.registration.SystemEmailTested = false }, want: ErrClosed},
		{name: "link deleted", change: func(m *completeMemory) { m.links = nil }, want: ErrLinkInvalid},
		// Another submission of the link created the account and deleted it.
		{name: "account created", change: func(m *completeMemory) { m.accounts = []string{visitor}; m.links = nil }, want: ErrAccountExists},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage, secret := completeSetup(t)
			storage.beforeLock = c.change

			_, err := testCompletion(storage).Complete(t.Context(), secret, testPassword)

			require.ErrorIs(t, err, c.want)
			assert.Equal(t, 1, storage.locked)
			assert.Empty(t, storage.members)
		})
	}
}

// The link is checked with the clock read under the lock.
func TestCompleteExpiresWhileHashing(t *testing.T) {
	storage, secret := completeSetup(t)
	now := testNow
	storage.beforeLock = func(*completeMemory) { now = testNow.Add(linkLifetime) }

	_, err := NewCompletion(storage, func() time.Time { return now }).Complete(t.Context(), secret, testPassword)

	require.ErrorIs(t, err, ErrLinkInvalid)
	assert.Empty(t, storage.members)
}

func TestCompleteStorageError(t *testing.T) {
	storage, secret := completeSetup(t)
	storage.readErr = errors.New("database down")

	_, err := testCompletion(storage).Complete(t.Context(), secret, testPassword)

	require.ErrorIs(t, err, storage.readErr)
	assert.Zero(t, storage.locked)
}

// A completion whose context ends before the hash gives up before storage
// takes its locks.
func TestCompleteEndedContext(t *testing.T) {
	storage, secret := completeSetup(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := testCompletion(storage).Complete(ctx, secret, testPassword)

	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, storage.locked)
}
