package connectedaccount

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/encryption"
)

func (m *memoryStorage) ReadConnectedAccountAccess(_ context.Context, accountID int64) (AccessState, error) {
	i := m.findID(accountID)
	if i < 0 {
		return AccessState{}, ErrNotFound
	}
	s := m.accounts[i]
	return AccessState{
		UserID:         s.account.UserID,
		Credential:     s.credential,
		NeedsReconnect: s.account.NeedsReconnectSince != nil,
		Deactivated:    m.deactivated[s.account.UserID],
	}, nil
}

func (m *memoryStorage) ReplaceConnectedAccountToken(_ context.Context, accountID int64, current []byte, next Credential) error {
	i := m.findCurrent(accountID, current)
	if i < 0 {
		return ErrCredentialChanged
	}
	m.accounts[i].credential = next
	m.accounts[i].account.NeedsReconnectSince = nil
	return nil
}

func (m *memoryStorage) MarkConnectedAccountReconnect(_ context.Context, accountID int64, current []byte, at time.Time) error {
	i := m.findCurrent(accountID, current)
	if i < 0 {
		return ErrCredentialChanged
	}
	if m.accounts[i].account.NeedsReconnectSince == nil {
		m.accounts[i].account.NeedsReconnectSince = &at
	}
	return nil
}

// findID returns the index of account accountID, whoever's it is, or -1.
func (m *memoryStorage) findID(accountID int64) int {
	for i, s := range m.accounts {
		if s.account.ID == accountID {
			return i
		}
	}
	return -1
}

// findCurrent returns the index of account accountID if its ciphertext is
// current, or -1.
func (m *memoryStorage) findCurrent(accountID int64, current []byte) int {
	i := m.findID(accountID)
	if i < 0 || !bytes.Equal(m.accounts[i].credential.Ciphertext, current) {
		return -1
	}
	return i
}

// fakeRefresher answers refresh tokens as Google's token endpoint would.
type fakeRefresher struct {
	// answer returns Google's answer to the nth refresh, from 0.
	answer func(n int) (Renewal, error)
	// refreshed are the refresh tokens sent, in order.
	refreshed []string
}

func (f *fakeRefresher) Refresh(_ context.Context, refreshToken string) (Renewal, error) {
	f.refreshed = append(f.refreshed, refreshToken)
	return f.answer(len(f.refreshed) - 1)
}

// accessTest is token access on memory storage with [alex] connected by
// [member] with refresh-1, a fake Google, and the clock it reads.
type accessTest struct {
	access   *Access
	accounts *Service
	storage  *memoryStorage
	ring     *encryption.Keyring
	google   *fakeRefresher
	account  Account
	now      time.Time
}

func newAccessTest(t *testing.T) *accessTest {
	t.Helper()
	service, storage, ring := newTestService(t)
	account, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)
	a := &accessTest{
		accounts: service,
		storage:  storage,
		ring:     ring,
		google: &fakeRefresher{answer: func(int) (Renewal, error) {
			return Renewal{AccessToken: NewAccessToken("access-1")}, nil
		}},
		account: account,
		now:     time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
	}
	a.access = NewAccess(storage, ring, a.google, func() time.Time { return a.now })
	return a
}

// reconnect replaces the account's refresh token with refreshToken, as a
// member's reconnect does.
func (a *accessTest) reconnect(t *testing.T, refreshToken string) {
	t.Helper()
	_, err := a.accounts.Reconnect(t.Context(), member, a.account.ID, alex, refreshToken)
	require.NoError(t, err)
}

// needsReconnectSince returns when the account started needing a
// reconnect, or nil.
func (a *accessTest) needsReconnectSince(t *testing.T) *time.Time {
	t.Helper()
	return a.storage.accounts[a.storage.find(member, a.account.ID)].account.NeedsReconnectSince
}

func TestToken(t *testing.T) {
	a := newAccessTest(t)
	before := a.storage.accounts[0].credential

	token, err := a.access.Token(t.Context(), a.account.ID)
	require.NoError(t, err)

	assert.Equal(t, "access-1", token.Value())
	assert.Equal(t, []string{"refresh-1"}, a.google.refreshed)
	assert.Equal(t, before, a.storage.accounts[0].credential, "no replacement, no write")
}

// A replacement Google sends is saved, sealed for the account, before the
// access token is handed out.
func TestTokenReplacement(t *testing.T) {
	a := newAccessTest(t)
	a.google.answer = func(int) (Renewal, error) {
		return Renewal{AccessToken: NewAccessToken("access-1"), RefreshToken: "refresh-2"}, nil
	}

	_, err := a.access.Token(t.Context(), a.account.ID)
	require.NoError(t, err)

	assert.Equal(t, "refresh-2", storedToken(t, a.ring, a.storage, member, a.account.ID))
}

func TestTokenRefused(t *testing.T) {
	a := newAccessTest(t)
	a.google.answer = func(int) (Renewal, error) { return Renewal{}, ErrRefreshRefused }

	_, err := a.access.Token(t.Context(), a.account.ID)

	require.ErrorIs(t, err, ErrNeedsReconnect)
	require.NotNil(t, a.needsReconnectSince(t))
	assert.Equal(t, a.now, *a.needsReconnectSince(t))
	assert.Equal(t, "refresh-1", storedToken(t, a.ring, a.storage, member, a.account.ID), "the refused token is kept")
}

// An account that needs reconnecting is not tried at Google again.
func TestTokenNeedsReconnect(t *testing.T) {
	a := newAccessTest(t)
	a.google.answer = func(int) (Renewal, error) { return Renewal{}, ErrRefreshRefused }
	_, err := a.access.Token(t.Context(), a.account.ID)
	require.ErrorIs(t, err, ErrNeedsReconnect)

	_, err = a.access.Token(t.Context(), a.account.ID)

	require.ErrorIs(t, err, ErrNeedsReconnect)
	assert.Len(t, a.google.refreshed, 1)
}

// A refusal of a token a reconnect has since replaced is not recorded: the
// new token is tried instead.
func TestTokenRefusedRace(t *testing.T) {
	a := newAccessTest(t)
	a.google.answer = func(n int) (Renewal, error) {
		if n == 0 {
			a.reconnect(t, "refresh-2")
			return Renewal{}, ErrRefreshRefused
		}
		return Renewal{AccessToken: NewAccessToken("access-2")}, nil
	}

	token, err := a.access.Token(t.Context(), a.account.ID)
	require.NoError(t, err)

	assert.Equal(t, "access-2", token.Value())
	assert.Equal(t, []string{"refresh-1", "refresh-2"}, a.google.refreshed)
	assert.Nil(t, a.needsReconnectSince(t))
}

// A replacement for a token a reconnect has since replaced is dropped, and
// the reconnect's token is kept.
func TestTokenReplacedRace(t *testing.T) {
	a := newAccessTest(t)
	a.google.answer = func(n int) (Renewal, error) {
		if n == 0 {
			a.reconnect(t, "refresh-3")
			return Renewal{AccessToken: NewAccessToken("access-1"), RefreshToken: "refresh-2"}, nil
		}
		return Renewal{AccessToken: NewAccessToken("access-3")}, nil
	}

	token, err := a.access.Token(t.Context(), a.account.ID)
	require.NoError(t, err)

	assert.Equal(t, "access-3", token.Value())
	assert.Equal(t, "refresh-3", storedToken(t, a.ring, a.storage, member, a.account.ID))
}

// A credential that keeps changing is read again only once.
func TestTokenChangedTwice(t *testing.T) {
	a := newAccessTest(t)
	a.google.answer = func(n int) (Renewal, error) {
		a.reconnect(t, fmt.Sprintf("refresh-%d", n+2))
		return Renewal{}, ErrRefreshRefused
	}

	_, err := a.access.Token(t.Context(), a.account.ID)

	require.ErrorIs(t, err, ErrCredentialChanged)
	assert.Len(t, a.google.refreshed, 2)
	assert.Nil(t, a.needsReconnectSince(t))
}

func TestTokenAccountGone(t *testing.T) {
	a := newAccessTest(t)
	_, err := a.accounts.Disconnect(t.Context(), member, a.account.ID)
	require.NoError(t, err)

	_, err = a.access.Token(t.Context(), a.account.ID)

	require.ErrorIs(t, err, ErrNotFound)
	assert.Empty(t, a.google.refreshed)
}

// An account disconnected while Google answers is not handed a token.
func TestTokenGoneOnReplace(t *testing.T) {
	a := newAccessTest(t)
	a.google.answer = func(int) (Renewal, error) {
		_, err := a.accounts.Disconnect(t.Context(), member, a.account.ID)
		require.NoError(t, err)
		return Renewal{AccessToken: NewAccessToken("access-1"), RefreshToken: "refresh-2"}, nil
	}

	_, err := a.access.Token(t.Context(), a.account.ID)

	require.ErrorIs(t, err, ErrNotFound)
}

// An account disconnected while Google answers is not handed a token, even
// with no replacement to save.
func TestTokenGoneMidway(t *testing.T) {
	a := newAccessTest(t)
	a.google.answer = func(int) (Renewal, error) {
		_, err := a.accounts.Disconnect(t.Context(), member, a.account.ID)
		require.NoError(t, err)
		return Renewal{AccessToken: NewAccessToken("access-1")}, nil
	}

	_, err := a.access.Token(t.Context(), a.account.ID)

	require.ErrorIs(t, err, ErrNotFound)
}

// A member deactivated while Google answers gets no provider work.
func TestTokenDeactivatedMidway(t *testing.T) {
	a := newAccessTest(t)
	a.google.answer = func(int) (Renewal, error) {
		a.storage.deactivated = map[int64]bool{member: true}
		return Renewal{AccessToken: NewAccessToken("access-1"), RefreshToken: "refresh-2"}, nil
	}

	_, err := a.access.Token(t.Context(), a.account.ID)

	require.ErrorIs(t, err, ErrDeactivated)
}

// A refusal another job recorded while Google answered this one, with no
// replacement, means the grant ended after this answer: no token.
func TestTokenRefusedMidway(t *testing.T) {
	a := newAccessTest(t)
	a.google.answer = func(int) (Renewal, error) {
		current := a.storage.accounts[0].credential.Ciphertext
		require.NoError(t, a.storage.MarkConnectedAccountReconnect(t.Context(), a.account.ID, current, a.now))
		return Renewal{AccessToken: NewAccessToken("access-1")}, nil
	}

	_, err := a.access.Token(t.Context(), a.account.ID)

	require.ErrorIs(t, err, ErrNeedsReconnect)
}

// When Google replaces a token, another job using the old one at the same
// moment may be refused and mark the account; the saved replacement still
// works, so the mark goes.
func TestTokenRotationRace(t *testing.T) {
	a := newAccessTest(t)
	a.google.answer = func(int) (Renewal, error) {
		current := a.storage.accounts[0].credential.Ciphertext
		require.NoError(t, a.storage.MarkConnectedAccountReconnect(t.Context(), a.account.ID, current, a.now))
		return Renewal{AccessToken: NewAccessToken("access-1"), RefreshToken: "refresh-2"}, nil
	}

	token, err := a.access.Token(t.Context(), a.account.ID)
	require.NoError(t, err)

	assert.Equal(t, "access-1", token.Value())
	assert.Nil(t, a.needsReconnectSince(t))
	assert.Equal(t, "refresh-2", storedToken(t, a.ring, a.storage, member, a.account.ID))
}

func TestTokenDeactivated(t *testing.T) {
	a := newAccessTest(t)
	a.storage.deactivated = map[int64]bool{member: true}

	_, err := a.access.Token(t.Context(), a.account.ID)

	require.ErrorIs(t, err, ErrDeactivated)
	assert.Empty(t, a.google.refreshed)
}

// A token that no key opens fails without reaching Google and without being
// recorded as needing a reconnect.
func TestTokenUnreadable(t *testing.T) {
	a := newAccessTest(t)
	other, err := encryption.New("1:AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI=")
	require.NoError(t, err)
	access := NewAccess(a.storage, other, a.google, func() time.Time { return a.now })

	_, err = access.Token(t.Context(), a.account.ID)

	require.ErrorIs(t, err, ErrTokenUnreadable)
	assert.Empty(t, a.google.refreshed)
	assert.Nil(t, a.needsReconnectSince(t))
}

// Any other failure at Google may pass, so it is not recorded.
func TestTokenGoogleFails(t *testing.T) {
	a := newAccessTest(t)
	unavailable := errors.New("google token endpoint: 503")
	a.google.answer = func(int) (Renewal, error) { return Renewal{}, unavailable }

	_, err := a.access.Token(t.Context(), a.account.ID)

	require.ErrorIs(t, err, unavailable)
	require.ErrorIs(t, err, ErrRefreshFailed, "a job can tell Google's failure from inbok's")
	require.NotErrorIs(t, err, ErrNeedsReconnect)
	assert.Nil(t, a.needsReconnectSince(t))
}

func TestTokenSealFails(t *testing.T) {
	a := newAccessTest(t)
	a.google.answer = func(int) (Renewal, error) {
		return Renewal{AccessToken: NewAccessToken("access-1"), RefreshToken: "refresh-2"}, nil
	}
	sealErr := errors.New("seal failed")
	access := NewAccess(a.storage, failingKeyring{Keyring: a.ring, err: sealErr}, a.google, func() time.Time { return a.now })

	_, err := access.Token(t.Context(), a.account.ID)

	require.ErrorIs(t, err, sealErr)
	require.NotErrorIs(t, err, ErrRefreshFailed, "inbok's own failure is not Google's")
	assert.Equal(t, "refresh-1", storedToken(t, a.ring, a.storage, member, a.account.ID))
}

// Check answers whether provider work may run, with Token's stop errors,
// and never asks Google.
func TestCheck(t *testing.T) {
	otherKeys, err := encryption.New("1:AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI=")
	require.NoError(t, err)
	for name, tc := range map[string]struct {
		stop func(a *accessTest) *Access
		want error
	}{
		"usable": {stop: func(a *accessTest) *Access { return a.access }},
		"deactivated": {stop: func(a *accessTest) *Access {
			a.storage.deactivated = map[int64]bool{member: true}
			return a.access
		}, want: ErrDeactivated},
		"needs reconnecting": {stop: func(a *accessTest) *Access {
			a.storage.accounts[a.storage.find(member, a.account.ID)].account.NeedsReconnectSince = &a.now
			return a.access
		}, want: ErrNeedsReconnect},
		"disconnected": {stop: func(a *accessTest) *Access {
			_, err := a.accounts.Disconnect(t.Context(), member, a.account.ID)
			require.NoError(t, err)
			return a.access
		}, want: ErrNotFound},
		"unreadable token": {stop: func(a *accessTest) *Access {
			return NewAccess(a.storage, otherKeys, a.google, func() time.Time { return a.now })
		}, want: ErrTokenUnreadable},
	} {
		t.Run(name, func(t *testing.T) {
			a := newAccessTest(t)
			access := tc.stop(a)

			err := access.Check(t.Context(), a.account.ID)

			if tc.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.want)
			}
			assert.Empty(t, a.google.refreshed)
		})
	}
}

// failingKeyring opens with Keyring but fails every seal with err.
type failingKeyring struct {
	*encryption.Keyring
	err error
}

func (f failingKeyring) Seal([]byte, string) (string, []byte, error) {
	return "", nil, f.err
}

// Printing an access token or a renewal, or saving one as JSON, as a job's
// step output is, shows no token.
func TestAccessTokenHidden(t *testing.T) {
	renewal := Renewal{AccessToken: NewAccessToken("access-1"), RefreshToken: "refresh-1"}

	saved, err := json.Marshal(renewal.AccessToken)
	require.NoError(t, err)
	savedRenewal, err := json.Marshal(renewal)
	require.NoError(t, err)
	printed := fmt.Sprintf("%v %+v %#v %v %+v %#v %#v %s %s", renewal, renewal, renewal, renewal.AccessToken,
		renewal.AccessToken, renewal.AccessToken, &renewal.AccessToken, saved, savedRenewal)

	assert.NotContains(t, printed, "access-1")
	assert.NotContains(t, printed, "refresh-1")
}
