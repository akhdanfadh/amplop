package connectedaccount

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/emailaddress"
	"inbok/internal/encryption"
)

// stored is one saved account with its credential, as the table keeps it.
type stored struct {
	account    Account
	credential Credential
}

// memoryStorage keeps accounts like the table: IDs count up, and a user has
// a subject at most once.
type memoryStorage struct {
	accounts []stored
	lastID   int64
	states   map[[sha256.Size]byte]ConnectState
	// deactivated are the deactivated members.
	deactivated map[int64]bool
	// removals are the disconnected accounts' tokens, by removal ID.
	removals      map[int64]storedRemoval
	lastRemovalID int64
}

// storedRemoval is a kept token as the table keeps it: with the Google
// account it belongs to.
type storedRemoval struct {
	removal Removal
	subject string
}

func (m *memoryStorage) InsertConnectedAccount(_ context.Context, account NewAccount, seal func(int64) (Credential, error)) (Account, error) {
	m.lastID++
	credential, err := seal(m.lastID)
	if err != nil {
		return Account{}, err
	}
	for _, s := range m.accounts {
		if s.account.UserID == account.UserID && s.account.Provider == account.Provider && s.account.Subject == account.Subject {
			return Account{}, ErrAlreadyConnected
		}
	}
	saved := Account{
		ID:       m.lastID,
		UserID:   account.UserID,
		Provider: account.Provider,
		Email:    account.Email,
		Subject:  account.Subject,
		// One second apart per ID, so creation order is ID order.
		CreatedAt: time.Date(2026, 11, 5, 9, 0, int(m.lastID), 0, time.UTC),
	}
	m.accounts = append(m.accounts, stored{account: saved, credential: credential})
	return saved, nil
}

func (m *memoryStorage) UpdateConnectedAccount(_ context.Context, userID, accountID int64, update func(Account) (Reconnection, error)) (Account, error) {
	i := m.find(userID, accountID)
	if i < 0 {
		return Account{}, ErrNotFound
	}
	next, err := update(m.accounts[i].account)
	if err != nil {
		return Account{}, err
	}
	m.accounts[i].account.Email = next.Email
	m.accounts[i].account.NeedsReconnectSince = nil
	m.accounts[i].credential = next.Credential
	return m.accounts[i].account, nil
}

func (m *memoryStorage) ListConnectedAccounts(_ context.Context, userID int64) ([]StoredAccount, error) {
	// Empty, not nil, as PostgreSQL storage returns it.
	accounts := []StoredAccount{}
	for _, s := range m.accounts {
		if s.account.UserID == userID {
			accounts = append(accounts, StoredAccount{Account: s.account, Credential: s.credential})
		}
	}
	return accounts, nil
}

func (m *memoryStorage) RenameConnectedAccount(_ context.Context, userID, accountID int64, name string) (StoredAccount, error) {
	i := m.find(userID, accountID)
	if i < 0 {
		return StoredAccount{}, ErrNotFound
	}
	m.accounts[i].account.Name = name
	return StoredAccount{Account: m.accounts[i].account, Credential: m.accounts[i].credential}, nil
}

func (m *memoryStorage) DeleteConnectedAccount(_ context.Context, userID, accountID int64) (StoredAccount, error) {
	i := m.find(userID, accountID)
	if i < 0 {
		return StoredAccount{}, ErrNotFound
	}
	deleted := StoredAccount{Account: m.accounts[i].account, Credential: m.accounts[i].credential}
	m.keepRemoval(userID, accountID)
	return deleted, nil
}

// keepRemoval deletes userID's account accountID and keeps its token as a
// removal, as a disconnect does, and returns the removal's ID.
func (m *memoryStorage) keepRemoval(userID, accountID int64) int64 {
	i := m.find(userID, accountID)
	deleted := m.accounts[i]
	m.accounts = append(m.accounts[:i], m.accounts[i+1:]...)
	if m.removals == nil {
		m.removals = map[int64]storedRemoval{}
	}
	m.lastRemovalID++
	m.removals[m.lastRemovalID] = storedRemoval{
		removal: Removal{UserID: userID, AccountID: accountID, Credential: deleted.credential},
		subject: deleted.account.Subject,
	}
	return m.lastRemovalID
}

// find returns the index of userID's account accountID, or -1.
func (m *memoryStorage) find(userID, accountID int64) int {
	for i, s := range m.accounts {
		if s.account.ID == accountID && s.account.UserID == userID {
			return i
		}
	}
	return -1
}

const (
	member       int64 = 7
	otherMember  int64 = 8
	alexSubject        = "100000000000000000001"
	blairSubject       = "100000000000000000002"
)

var (
	alex  = Identity{Subject: alexSubject, Email: "alex@example.com"}
	blair = Identity{Subject: blairSubject, Email: "blair@example.com"}
)

// newTestService returns a [Service] on empty memory storage and the keyring
// that seals its tokens.
func newTestService(t *testing.T) (*Service, *memoryStorage, *encryption.Keyring) {
	t.Helper()
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	storage := &memoryStorage{}
	return NewService(storage, ring), storage, ring
}

// storedToken returns the refresh token in storage's account accountID,
// opened as that account of userID.
func storedToken(t *testing.T, ring *encryption.Keyring, storage *memoryStorage, userID, accountID int64) string {
	t.Helper()
	i := storage.find(userID, accountID)
	require.GreaterOrEqual(t, i, 0, "account not stored")
	credential := storage.accounts[i].credential
	token, err := ring.Open(credential.KeyID, credential.Ciphertext, tokenOwner(userID, accountID))
	require.NoError(t, err)
	return string(token)
}

func TestConnect(t *testing.T) {
	service, storage, ring := newTestService(t)

	account, err := service.Connect(t.Context(), member, Identity{Subject: alexSubject, Email: " Alex@Example.com "}, "refresh-1")
	require.NoError(t, err)

	assert.Equal(t, member, account.UserID)
	assert.Equal(t, ProviderGmail, account.Provider)
	assert.Equal(t, "alex@example.com", account.Email)
	assert.Equal(t, alexSubject, account.Subject)
	assert.Nil(t, account.NeedsReconnectSince)
	assert.Equal(t, "refresh-1", storedToken(t, ring, storage, member, account.ID))
}

// The sealed token opens only as its own account of its own user.
func TestConnectTokenBinding(t *testing.T) {
	service, storage, ring := newTestService(t)
	account, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)
	credential := storage.accounts[0].credential

	_, err = ring.Open(credential.KeyID, credential.Ciphertext, tokenOwner(member, account.ID+1))
	require.Error(t, err, "another account")
	_, err = ring.Open(credential.KeyID, credential.Ciphertext, tokenOwner(otherMember, account.ID))
	require.Error(t, err, "another user")
}

func TestConnectTwice(t *testing.T) {
	service, storage, _ := newTestService(t)
	_, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)

	_, err = service.Connect(t.Context(), member, alex, "refresh-2")

	require.ErrorIs(t, err, ErrAlreadyConnected)
	assert.Len(t, storage.accounts, 1)
}

// Two members may each connect the same Google account, each with their
// own token.
func TestConnectSameAccountTwoMembers(t *testing.T) {
	service, storage, ring := newTestService(t)
	first, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)

	second, err := service.Connect(t.Context(), otherMember, alex, "refresh-2")
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID)
	assert.Equal(t, "refresh-1", storedToken(t, ring, storage, member, first.ID))
	assert.Equal(t, "refresh-2", storedToken(t, ring, storage, otherMember, second.ID))
}

func TestConnectRefused(t *testing.T) {
	cases := []struct {
		name     string
		identity Identity
		token    string
		want     error
	}{
		{"invalid email", Identity{Subject: alexSubject, Email: "Alex <alex@example.com>"}, "refresh-1", emailaddress.ErrInvalid},
		{"no subject", Identity{Email: "alex@example.com"}, "refresh-1", errNoSubject},
		{"no refresh token", alex, "", errNoRefreshToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, storage, _ := newTestService(t)

			_, err := service.Connect(t.Context(), member, tc.identity, tc.token)

			require.ErrorIs(t, err, tc.want)
			assert.Empty(t, storage.accounts)
		})
	}
}

func TestReconnect(t *testing.T) {
	// Given an account whose token Google refused.
	service, storage, ring := newTestService(t)
	account, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)
	refused := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	storage.accounts[0].account.NeedsReconnectSince = &refused

	// When the member reconnects it, and Google now reports another email.
	renamed := Identity{Subject: alexSubject, Email: "alex.rivera@example.com"}
	reconnected, err := service.Reconnect(t.Context(), member, account.ID, renamed, "refresh-2")
	require.NoError(t, err)

	// Then the account has the new token and email and works again.
	assert.Equal(t, account.ID, reconnected.ID)
	assert.Equal(t, "alex.rivera@example.com", reconnected.Email)
	assert.Nil(t, reconnected.NeedsReconnectSince)
	assert.Equal(t, "refresh-2", storedToken(t, ring, storage, member, account.ID))
}

func TestReconnectDifferentAccount(t *testing.T) {
	service, storage, ring := newTestService(t)
	account, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)

	_, err = service.Reconnect(t.Context(), member, account.ID, blair, "refresh-2")

	require.ErrorIs(t, err, ErrDifferentAccount)
	assert.Equal(t, "alex@example.com", storage.accounts[0].account.Email)
	assert.Equal(t, "refresh-1", storedToken(t, ring, storage, member, account.ID))
}

// A member cannot reconnect, or learn of, another member's account.
func TestReconnectOtherMembersAccount(t *testing.T) {
	service, storage, ring := newTestService(t)
	account, err := service.Connect(t.Context(), otherMember, alex, "refresh-1")
	require.NoError(t, err)

	_, err = service.Reconnect(t.Context(), member, account.ID, alex, "refresh-2")

	require.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, "refresh-1", storedToken(t, ring, storage, otherMember, account.ID))
}

func TestReconnectNoRefreshToken(t *testing.T) {
	service, storage, ring := newTestService(t)
	account, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)

	_, err = service.Reconnect(t.Context(), member, account.ID, alex, "")

	require.ErrorIs(t, err, errNoRefreshToken)
	assert.Equal(t, "refresh-1", storedToken(t, ring, storage, member, account.ID))
}

func TestList(t *testing.T) {
	service, _, _ := newTestService(t)
	first, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)
	_, err = service.Connect(t.Context(), otherMember, alex, "refresh-2")
	require.NoError(t, err)
	second, err := service.Connect(t.Context(), member, blair, "refresh-3")
	require.NoError(t, err)

	accounts, err := service.List(t.Context(), member)
	require.NoError(t, err)

	assert.Equal(t, []Account{first, second}, accounts)
}

// An account's status is worked out at each read: a refused token needs a
// reconnect, and one no configured key opens is unreadable until a key that
// opens it is back.
func TestListStatus(t *testing.T) {
	service, storage, _ := newTestService(t)
	_, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)
	_, err = service.Connect(t.Context(), member, blair, "refresh-2")
	require.NoError(t, err)
	refused := time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)
	storage.accounts[1].account.NeedsReconnectSince = &refused
	otherKey, err := encryption.New("2:AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI=")
	require.NoError(t, err)

	statuses := func(service *Service) []Status {
		accounts, err := service.List(t.Context(), member)
		require.NoError(t, err)
		var got []Status
		for _, account := range accounts {
			got = append(got, account.Status)
		}
		return got
	}

	assert.Equal(t, []Status{StatusUsable, StatusNeedsReconnect}, statuses(service))
	assert.Equal(t, []Status{StatusTokenUnreadable, StatusNeedsReconnect}, statuses(NewService(storage, otherKey)),
		"a key list without the sealing key")
	assert.Equal(t, []Status{StatusUsable, StatusNeedsReconnect}, statuses(service), "the key back")
}

func TestCheckToken(t *testing.T) {
	service, storage, _ := newTestService(t)
	account, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)
	credential := storage.accounts[0].credential

	require.NoError(t, service.CheckToken(member, account.ID, credential))
	// The token is bound to its member and account.
	require.ErrorIs(t, service.CheckToken(otherMember, account.ID, credential), ErrTokenUnreadable)
	require.ErrorIs(t, service.CheckToken(member, account.ID+1, credential), ErrTokenUnreadable)
}

// The actions offered are the ones the services accept: an unreadable token
// still syncs, and a provider without a client refuses sync and reconnect.
func TestAccountAllowedActions(t *testing.T) {
	refused := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name    string
		account Account
		offered bool
		want    []Action
	}{
		{"usable", Account{Status: StatusUsable}, true, []Action{ActionSync, ActionReconnect, ActionRename, ActionDisconnect}},
		{"token unreadable", Account{Status: StatusTokenUnreadable}, true, []Action{ActionSync, ActionReconnect, ActionRename, ActionDisconnect}},
		{"needs reconnect", Account{Status: StatusNeedsReconnect, NeedsReconnectSince: &refused}, true, []Action{ActionReconnect, ActionRename, ActionDisconnect}},
		{"provider not offered", Account{Status: StatusUsable}, false, []Action{ActionRename, ActionDisconnect}},
		{"needs reconnect, not offered", Account{Status: StatusNeedsReconnect, NeedsReconnectSince: &refused}, false, []Action{ActionRename, ActionDisconnect}},
	} {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, c.account.AllowedActions(c.offered))
		})
	}
}

func TestListEmpty(t *testing.T) {
	service, _, _ := newTestService(t)

	accounts, err := service.List(t.Context(), member)
	require.NoError(t, err)

	assert.NotNil(t, accounts)
	assert.Empty(t, accounts)
}

func TestRename(t *testing.T) {
	service, storage, _ := newTestService(t)
	account, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)

	renamed, err := service.Rename(t.Context(), member, account.ID, "  Studio  ")
	require.NoError(t, err)

	account.Name = "Studio"
	assert.Equal(t, account, renamed, "trimmed, and usable as before")
	assert.Equal(t, "Studio", storage.accounts[0].account.Name)
}

func TestRenameClears(t *testing.T) {
	service, storage, _ := newTestService(t)
	account, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)
	_, err = service.Rename(t.Context(), member, account.ID, "Studio")
	require.NoError(t, err)

	renamed, err := service.Rename(t.Context(), member, account.ID, " \t ")

	require.NoError(t, err)
	assert.Empty(t, renamed.Name)
	assert.Empty(t, storage.accounts[0].account.Name)
}

func TestRenameRefused(t *testing.T) {
	cases := map[string]struct {
		name string
		want error
	}{
		"too long":        {strings.Repeat("é", MaxNameLength+1), ErrNameTooLong},
		"a line break":    {"Stu\ndio", ErrNameInvalid},
		"a control":       {"Stu\u0007dio", ErrNameInvalid},
		"no such account": {"Studio", ErrNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			service, storage, _ := newTestService(t)
			account, err := service.Connect(t.Context(), otherMember, alex, "refresh-1")
			require.NoError(t, err)
			id := account.ID
			if tc.want != ErrNotFound {
				account, err = service.Connect(t.Context(), member, blair, "refresh-2")
				require.NoError(t, err)
				id = account.ID
			}

			_, err = service.Rename(t.Context(), member, id, tc.name)

			require.ErrorIs(t, err, tc.want)
			for _, s := range storage.accounts {
				assert.Empty(t, s.account.Name, "nothing is saved")
			}
		})
	}
}

func TestParseNameLength(t *testing.T) {
	name, err := parseName(strings.Repeat("é", MaxNameLength))

	require.NoError(t, err, "the limit counts characters, not bytes")
	assert.Equal(t, strings.Repeat("é", MaxNameLength), name)
}

func TestDisconnect(t *testing.T) {
	service, storage, _ := newTestService(t)
	account, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)

	disconnected, err := service.Disconnect(t.Context(), member, account.ID)
	require.NoError(t, err)

	assert.Equal(t, account, disconnected)
	assert.Empty(t, storage.accounts)
	require.Len(t, storage.removals, 1, "the token is kept for revoking")
}

func TestDisconnectOtherMembersAccount(t *testing.T) {
	service, storage, _ := newTestService(t)
	account, err := service.Connect(t.Context(), otherMember, alex, "refresh-1")
	require.NoError(t, err)

	_, err = service.Disconnect(t.Context(), member, account.ID)

	require.ErrorIs(t, err, ErrNotFound)
	assert.Len(t, storage.accounts, 1)
	assert.Empty(t, storage.removals)
}

// failingSealer refuses to seal or open, as a keyring cannot seal today but
// another sealer may.
type failingSealer struct{ err error }

func (f failingSealer) Seal([]byte, string) (string, []byte, error) {
	return "", nil, f.err
}

func (f failingSealer) Open(string, []byte, string) ([]byte, error) {
	return nil, f.err
}

func TestConnectSealFails(t *testing.T) {
	broken := errors.New("seal failed")
	storage := &memoryStorage{}
	service := NewService(storage, failingSealer{err: broken})

	_, err := service.Connect(t.Context(), member, alex, "refresh-1")

	require.ErrorIs(t, err, broken)
	assert.Empty(t, storage.accounts)
}
