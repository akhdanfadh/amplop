package passwordreset

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/emailaddress"
	"inbok/internal/passwordhash"
	"inbok/internal/randomsecret"
	"inbok/internal/systememail"
)

const testPassword = "correct horse battery"

var (
	testNow  = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	testUser = account.User{ID: 7, Email: "member@example.com", Role: account.RoleMember}
)

// testPublicURL is the public URL test links start with.
var testPublicURL = &url.URL{Scheme: "https", Host: "mail.example.com"}

// fakeMailer records what it is asked to send and fails with err.
type fakeMailer struct {
	mu   sync.Mutex
	err  error
	sent []systememail.Outgoing
}

// Send records outgoing and returns revision 1 with err.
func (f *fakeMailer) Send(_ context.Context, outgoing systememail.Outgoing) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, outgoing)
	return 1, f.err
}

// completeMemory keeps users and links like the database does.
type completeMemory struct {
	user        account.User
	deactivated bool
	links       []Link
	changes     []CompleteChange
	readErr     error
	locked      int // calls to CompletePasswordReset

	// beforeLock runs before CompletePasswordReset reads, like a write that
	// commits while the password is hashed.
	beforeLock func(*completeMemory)
}

// state returns the [CompleteState] for the link with hash.
func (m *completeMemory) state(hash [sha256.Size]byte) CompleteState {
	at := slices.IndexFunc(m.links, func(l Link) bool { return l.TokenHash == hash })
	if at < 0 {
		return CompleteState{}
	}
	link := m.links[at]
	return CompleteState{Link: &link, User: m.user, Deactivated: m.deactivated}
}

// ReadPasswordReset returns the state for hash, or readErr.
func (m *completeMemory) ReadPasswordReset(_ context.Context, hash [sha256.Size]byte) (CompleteState, error) {
	return m.state(hash), m.readErr
}

// CompletePasswordReset runs beforeLock, then calls complete with the state
// for hash. If complete succeeds, it saves the change, deletes the user's
// links, and returns the user.
func (m *completeMemory) CompletePasswordReset(_ context.Context, hash [sha256.Size]byte, complete func(CompleteState) (CompleteChange, error)) (account.User, error) {
	m.locked++
	if m.beforeLock != nil {
		m.beforeLock(m)
	}
	state := m.state(hash)
	change, err := complete(state)
	if err != nil {
		return account.User{}, err
	}
	m.changes = append(m.changes, change)
	m.links = slices.DeleteFunc(m.links, func(l Link) bool { return l.UserID == state.User.ID })
	return state.User, nil
}

// completeSetup returns [testUser] with one live link, and the link's secret.
func completeSetup(t *testing.T) (*completeMemory, string) {
	t.Helper()

	secret, hash := randomsecret.New()
	return &completeMemory{
		user:  testUser,
		links: []Link{{TokenHash: hash, UserID: testUser.ID, Email: testUser.Email, CreatedAt: testNow, ExpiresAt: testNow.Add(linkLifetime)}},
	}, secret
}

// testCompletion returns a [Completion] over storage whose clock reads
// [testNow].
func testCompletion(t *testing.T, storage *completeMemory) *Completion {
	t.Helper()
	return NewCompletion(storage, func() time.Time { return testNow })
}

func TestComplete(t *testing.T) {
	storage, secret := completeSetup(t)
	_, otherHash := randomsecret.New()
	storage.links = append(storage.links, Link{TokenHash: otherHash, UserID: testUser.ID, Email: testUser.Email, ExpiresAt: testNow.Add(linkLifetime)})

	user, err := testCompletion(t, storage).Complete(t.Context(), secret, testUser.Email, testPassword)
	require.NoError(t, err)

	assert.Equal(t, testUser, user)
	require.Len(t, storage.changes, 1)
	ok, err := passwordhash.Verify(t.Context(), testPassword, storage.changes[0].PasswordHash)
	require.NoError(t, err)
	assert.True(t, ok, "the hash is of the password")
	assert.Empty(t, storage.links, "every link of the user is gone")
}

// Each refusal comes before the hash and the lock.
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
		{name: "deactivated", change: func(m *completeMemory) { m.deactivated = true }, want: ErrLinkInvalid},
		{name: "email changed", change: func(m *completeMemory) { m.user.Email = "new@example.com" }, want: ErrLinkInvalid},
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

			_, err := testCompletion(t, storage).Complete(t.Context(), secret, testUser.Email, password)

			require.ErrorIs(t, err, c.want)
			assert.Zero(t, storage.locked)
			assert.Empty(t, storage.changes, "a refusal changes nothing and saves no notice")
		})
	}
}

// What commits while the password is hashed is checked again under the
// lock.
func TestCompleteRechecks(t *testing.T) {
	cases := []struct {
		name   string
		change func(*completeMemory)
	}{
		{"link used", func(m *completeMemory) { m.links = nil }},
		{"deactivated", func(m *completeMemory) { m.deactivated = true }},
		{"email changed", func(m *completeMemory) { m.user.Email = "new@example.com" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage, secret := completeSetup(t)
			storage.beforeLock = c.change

			_, err := testCompletion(t, storage).Complete(t.Context(), secret, testUser.Email, testPassword)

			require.ErrorIs(t, err, ErrLinkInvalid)
			assert.Equal(t, 1, storage.locked)
			assert.Empty(t, storage.changes)
		})
	}
}

func TestCompleteStorageError(t *testing.T) {
	storage, secret := completeSetup(t)
	storage.readErr = errors.New("database down")

	_, err := testCompletion(t, storage).Complete(t.Context(), secret, testUser.Email, testPassword)

	require.ErrorIs(t, err, storage.readErr)
	assert.Zero(t, storage.locked)
}

// A completion whose context ends before the hash gives up before storage
// takes its lock.
func TestCompleteEndedContext(t *testing.T) {
	storage, secret := completeSetup(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := testCompletion(t, storage).Complete(ctx, secret, testUser.Email, testPassword)

	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, storage.locked)
}

// A link that expires while the password is hashed is refused under the
// lock.
func TestCompleteExpiresDuringHash(t *testing.T) {
	storage, secret := completeSetup(t)
	reads := 0
	clock := func() time.Time {
		reads++
		if reads == 1 {
			return testNow
		}
		return testNow.Add(linkLifetime)
	}

	completion := NewCompletion(storage, clock)

	_, err := completion.Complete(t.Context(), secret, testUser.Email, testPassword)

	require.ErrorIs(t, err, ErrLinkInvalid)
	assert.Equal(t, 1, storage.locked)
	assert.Empty(t, storage.changes)
}

// A reset carries the time it was checked under the lock, the clock's
// second reading, which the notice to the owner names.
func TestCompleteChangedAt(t *testing.T) {
	storage, secret := completeSetup(t)
	reads := 0
	clock := func() time.Time {
		reads++
		return testNow.Add(time.Duration(reads) * time.Minute)
	}

	_, err := NewCompletion(storage, clock).Complete(t.Context(), secret, testUser.Email, testPassword)

	require.NoError(t, err)
	require.Len(t, storage.changes, 1)
	assert.Equal(t, testNow.Add(2*time.Minute), storage.changes[0].ChangedAt)
}

// The email given must be the one the link was sent to, however it is
// spelled.
func TestCompleteEmailSpelling(t *testing.T) {
	storage, secret := completeSetup(t)

	_, err := testCompletion(t, storage).Complete(t.Context(), secret, " Member@Example.COM ", testPassword)

	require.NoError(t, err)
	assert.Len(t, storage.changes, 1)
}

// Another email cannot reset the account, so a link sent for an account
// someone controls fails for a person who types their own address. A link
// that is not live answers as one before the email is compared, so an old
// link tells nothing more about the account.
func TestCompleteEmailRefused(t *testing.T) {
	cases := []struct {
		name   string
		email  string
		change func(*completeMemory)
		want   error
	}{
		{name: "other email", email: "alice@example.com", want: ErrEmailMismatch},
		{name: "invalid email", email: "not an email", want: emailaddress.ErrInvalid},
		{name: "used link, other email", email: "alice@example.com", change: func(m *completeMemory) { m.links = nil }, want: ErrLinkInvalid},
		{name: "expired link, other email", email: "alice@example.com", change: func(m *completeMemory) { m.links[0].ExpiresAt = testNow }, want: ErrLinkInvalid},
		{name: "deactivated, other email", email: "alice@example.com", change: func(m *completeMemory) { m.deactivated = true }, want: ErrLinkInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage, secret := completeSetup(t)
			if c.change != nil {
				c.change(storage)
			}

			_, err := testCompletion(t, storage).Complete(t.Context(), secret, c.email, testPassword)

			require.ErrorIs(t, err, c.want)
			assert.Zero(t, storage.locked, "refused before the hash and the lock")
			assert.Empty(t, storage.changes)
		})
	}
}
