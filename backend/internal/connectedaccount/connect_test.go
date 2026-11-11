package connectedaccount

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/attemptlimit"
	"inbok/internal/encryption"
	"inbok/internal/randomsecret"
)

func (m *memoryStorage) InsertConnectState(_ context.Context, userID int64, create func([]time.Time) (ConnectState, error)) error {
	if m.deactivated[userID] {
		return ErrDeactivated
	}
	var started []time.Time
	for _, state := range m.states {
		if state.UserID == userID {
			started = append(started, state.CreatedAt)
		}
	}
	state, err := create(started)
	if err != nil {
		return err
	}
	if m.states == nil {
		m.states = map[[sha256.Size]byte]ConnectState{}
	}
	state.UserID = userID
	m.states[state.StateHash] = state
	return nil
}

func (m *memoryStorage) DeleteConnectState(_ context.Context, hash [sha256.Size]byte) (ConnectState, error) {
	state, ok := m.states[hash]
	if !ok {
		return ConnectState{}, ErrStateInvalid
	}
	delete(m.states, hash)
	return state, nil
}

// fakeGoogle answers as Google would for one consent: the grant it hands
// out and the mailbox the grant's token reaches.
type fakeGoogle struct {
	grant       Grant
	mailbox     string
	exchangeErr error
	profileErr  error

	// What the flow sent.
	state, verifier, code, exchangeVerifier string
	accessToken                             AccessToken
}

func (f *fakeGoogle) AuthCodeURL(state, verifier string) string {
	f.state, f.verifier = state, verifier
	return "https://accounts.example/consent?state=" + url.QueryEscape(state)
}

func (f *fakeGoogle) Exchange(_ context.Context, code, verifier string) (Grant, error) {
	f.code, f.exchangeVerifier = code, verifier
	return f.grant, f.exchangeErr
}

func (f *fakeGoogle) Profile(_ context.Context, accessToken AccessToken) (Profile, error) {
	f.accessToken = accessToken
	return Profile{Email: f.mailbox}, f.profileErr
}

const testCode = "code-1"

// alexGrant is a grant for [alex] with everything inbok needs.
func alexGrant() Grant {
	return Grant{RefreshToken: "refresh-1", AccessToken: NewAccessToken("access-1"), Identity: alex, EmailVerified: true, GmailGranted: true}
}

// connectTest is a flow on memory storage with a fake Google, and the clock
// it reads.
type connectTest struct {
	connections *Connections
	storage     *memoryStorage
	ring        *encryption.Keyring
	google      *fakeGoogle
	accounts    *Service
	now         time.Time
	session     uuid.UUID
}

func newConnectTest(t *testing.T) *connectTest {
	t.Helper()
	service, storage, ring := newTestService(t)
	c := &connectTest{
		storage:  storage,
		ring:     ring,
		google:   &fakeGoogle{grant: alexGrant(), mailbox: "alex@example.com"},
		accounts: service,
		now:      time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
		session:  uuid.New(),
	}
	c.connections = NewConnections(storage, service, c.google, func() time.Time { return c.now })
	return c
}

// start starts the flow for [member]'s session, for accountID, and returns
// the state Google would send back.
func (c *connectTest) start(t *testing.T, accountID int64) string {
	t.Helper()
	_, err := c.connections.Start(t.Context(), member, c.session, accountID)
	require.NoError(t, err)
	return c.google.state
}

// finish finishes the flow for [member]'s session with state and [testCode].
func (c *connectTest) finish(t *testing.T, state string) (Account, error) {
	t.Helper()
	return c.connections.Finish(t.Context(), member, c.session, Callback{State: state, Code: testCode})
}

func TestStart(t *testing.T) {
	c := newConnectTest(t)

	consent, err := c.connections.Start(t.Context(), member, c.session, 0)
	require.NoError(t, err)

	assert.Equal(t, "https://accounts.example/consent?state="+c.google.state, consent)
	hash, err := randomsecret.Hash(c.google.state)
	require.NoError(t, err)
	saved := c.storage.states[hash]
	assert.Equal(t, member, saved.UserID)
	assert.Equal(t, c.session, saved.SessionID)
	assert.Zero(t, saved.AccountID)
	assert.Equal(t, c.google.verifier, saved.CodeVerifier)
	assert.NotEqual(t, c.google.state, c.google.verifier)
	assert.Equal(t, c.now, saved.CreatedAt)
	assert.Equal(t, c.now.Add(stateLifetime), saved.ExpiresAt)
}

func TestStartNoSession(t *testing.T) {
	c := newConnectTest(t)

	_, err := c.connections.Start(t.Context(), member, uuid.UUID{}, 0)

	require.ErrorIs(t, err, errNoSession)
	assert.Empty(t, c.storage.states)
}

// A member may have 5 unfinished starts; the sixth waits until the oldest
// expires, and a finished flow frees its place at once.
func TestStartThrottled(t *testing.T) {
	c := newConnectTest(t)
	started := c.now
	var first string
	for i := range startLimit.Max {
		state := c.start(t, 0)
		if i == 0 {
			first = state
		}
		c.now = c.now.Add(time.Minute)
	}

	_, err := c.connections.Start(t.Context(), member, c.session, 0)

	refusal, ok := errors.AsType[*attemptlimit.Error](err)
	require.True(t, ok, "got %v", err)
	require.ErrorIs(t, err, ErrStartThrottled)
	assert.Equal(t, started.Add(stateLifetime).Sub(c.now), refusal.Wait)
	assert.Len(t, c.storage.states, startLimit.Max)

	t.Run("finished flow frees a place", func(t *testing.T) {
		_, err := c.finish(t, first)
		require.NoError(t, err)

		c.start(t, 0)
	})
}

// A start counts until its state expires, even while the row is kept: one
// more fits only once the oldest has expired.
func TestStartThrottleExpiry(t *testing.T) {
	for _, c := range []struct {
		name   string
		after  time.Duration
		allows bool
	}{
		{"just before expiry", stateLifetime - time.Nanosecond, false},
		{"at expiry", stateLifetime, true},
		{"after expiry", stateLifetime + time.Minute, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			conn := newConnectTest(t)
			started := conn.now
			for range startLimit.Max {
				conn.start(t, 0)
			}
			conn.now = started.Add(c.after)

			_, err := conn.connections.Start(t.Context(), member, conn.session, 0)

			if c.allows {
				require.NoError(t, err)
				assert.Len(t, conn.storage.states, startLimit.Max+1, "the expired states are still kept")
				return
			}
			refusal, ok := errors.AsType[*attemptlimit.Error](err)
			require.True(t, ok, "got %v", err)
			assert.Equal(t, time.Nanosecond, refusal.Wait)
		})
	}
}

// Another member's starts do not count toward a member's limit.
func TestStartThrottlePerMember(t *testing.T) {
	c := newConnectTest(t)
	for range startLimit.Max {
		_, err := c.connections.Start(t.Context(), otherMember, uuid.New(), 0)
		require.NoError(t, err)
	}

	c.start(t, 0)
}

func TestStartDeactivated(t *testing.T) {
	c := newConnectTest(t)
	c.storage.deactivated = map[int64]bool{member: true}

	_, err := c.connections.Start(t.Context(), member, c.session, 0)

	require.ErrorIs(t, err, ErrDeactivated)
	assert.Empty(t, c.storage.states)
}

func TestStartReconnectOtherMembersAccount(t *testing.T) {
	c := newConnectTest(t)
	account, err := c.accounts.Connect(t.Context(), otherMember, alex, "refresh-0")
	require.NoError(t, err)

	_, err = c.connections.Start(t.Context(), member, c.session, account.ID)

	require.ErrorIs(t, err, ErrNotFound)
	assert.Empty(t, c.storage.states)
}

func TestFinishConnects(t *testing.T) {
	c := newConnectTest(t)
	state := c.start(t, 0)

	account, err := c.finish(t, state)
	require.NoError(t, err)

	assert.Equal(t, "alex@example.com", account.Email)
	assert.Equal(t, alexSubject, account.Subject)
	assert.Equal(t, testCode, c.google.code)
	assert.Equal(t, c.google.verifier, c.google.exchangeVerifier)
	assert.Equal(t, "access-1", c.google.accessToken.Value())
	assert.Equal(t, "refresh-1", storedToken(t, c.ring, c.storage, member, account.ID), "the refresh token is saved, not the access token")
	assert.Empty(t, c.storage.states, "the state is used up")
}

func TestFinishReconnects(t *testing.T) {
	c := newConnectTest(t)
	account, err := c.accounts.Connect(t.Context(), member, alex, "refresh-0")
	require.NoError(t, err)
	state := c.start(t, account.ID)

	reconnected, err := c.finish(t, state)
	require.NoError(t, err)

	assert.Equal(t, account.ID, reconnected.ID)
	assert.Len(t, c.storage.accounts, 1)
	assert.Equal(t, "refresh-1", storedToken(t, c.ring, c.storage, member, account.ID))
}

func TestFinishReconnectDifferentAccount(t *testing.T) {
	c := newConnectTest(t)
	account, err := c.accounts.Connect(t.Context(), member, blair, "refresh-0")
	require.NoError(t, err)
	state := c.start(t, account.ID)

	_, err = c.finish(t, state)

	require.ErrorIs(t, err, ErrDifferentAccount)
	assert.Equal(t, "refresh-0", storedToken(t, c.ring, c.storage, member, account.ID))
}

// An account disconnected between the callback taking its state and the
// save is not found. With PostgreSQL, a disconnect before that deletes the
// state, which then reads as invalid.
func TestFinishReconnectedAccountGone(t *testing.T) {
	c := newConnectTest(t)
	account, err := c.accounts.Connect(t.Context(), member, alex, "refresh-0")
	require.NoError(t, err)
	state := c.start(t, account.ID)
	_, err = c.accounts.Disconnect(t.Context(), member, account.ID)
	require.NoError(t, err)

	_, err = c.finish(t, state)

	require.ErrorIs(t, err, ErrNotFound)
}

func TestFinishAlreadyConnected(t *testing.T) {
	c := newConnectTest(t)
	account, err := c.accounts.Connect(t.Context(), member, alex, "refresh-0")
	require.NoError(t, err)
	state := c.start(t, 0)

	_, err = c.finish(t, state)

	require.ErrorIs(t, err, ErrAlreadyConnected)
	assert.Equal(t, "refresh-0", storedToken(t, c.ring, c.storage, member, account.ID))
}

// A state works only in the session and for the member that started it,
// and only before it expires. A refused state is used up, and nothing
// reaches Google.
func TestFinishStateRefused(t *testing.T) {
	cases := []struct {
		name    string
		userID  int64
		session func(c *connectTest) uuid.UUID
		state   func(state string) string
		advance time.Duration
	}{
		{name: "unknown state", userID: member, state: func(string) string {
			other, _ := randomsecret.New()
			return other
		}},
		{name: "malformed state", userID: member, state: func(string) string { return "not-a-state" }},
		{name: "another session", userID: member, session: func(*connectTest) uuid.UUID { return uuid.New() }},
		{name: "another member", userID: otherMember},
		{name: "expired", userID: member, advance: stateLifetime},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newConnectTest(t)
			state := c.start(t, 0)
			c.now = c.now.Add(tc.advance)
			session := c.session
			if tc.session != nil {
				session = tc.session(c)
			}
			if tc.state != nil {
				state = tc.state(state)
			}

			_, err := c.connections.Finish(t.Context(), tc.userID, session, Callback{State: state, Code: testCode})

			require.ErrorIs(t, err, ErrStateInvalid)
			assert.Empty(t, c.google.code, "nothing reached Google")
			assert.Empty(t, c.storage.accounts)
			if tc.state == nil {
				assert.Empty(t, c.storage.states, "the refused state is used up")
			}
		})
	}
}

func TestFinishStateUsedOnce(t *testing.T) {
	c := newConnectTest(t)
	state := c.start(t, 0)
	_, err := c.connections.Finish(t.Context(), member, c.session, Callback{State: state, Error: errorAccessDenied})
	require.ErrorIs(t, err, ErrCancelled)

	_, err = c.finish(t, state)

	require.ErrorIs(t, err, ErrStateInvalid)
	assert.Empty(t, c.storage.accounts)
}

// Each refusal saves nothing and uses the state up.
func TestFinishRefused(t *testing.T) {
	broken := errors.New("google unavailable")
	cases := []struct {
		name     string
		callback Callback
		google   func(*fakeGoogle)
		want     error
	}{
		{name: "cancelled at Google", callback: Callback{Error: errorAccessDenied}, want: ErrCancelled},
		{name: "other Google error", callback: Callback{Error: "invalid_scope"}, want: errAuthorization},
		{name: "no code", want: errNoCode},
		{name: "exchange refused", google: func(g *fakeGoogle) { g.exchangeErr = broken }, want: broken},
		{name: "no refresh token", google: func(g *fakeGoogle) { g.grant.RefreshToken = "" }, want: errNoGrantToken},
		{name: "Gmail unticked", google: func(g *fakeGoogle) { g.grant.GmailGranted = false }, want: ErrGmailNotGranted},
		{name: "email not verified", google: func(g *fakeGoogle) { g.grant.EmailVerified = false }, want: ErrEmailNotVerified},
		{name: "profile fails", google: func(g *fakeGoogle) { g.profileErr = broken }, want: broken},
		{name: "no Gmail", google: func(g *fakeGoogle) { g.profileErr = fmt.Errorf("%w: gmail api: 400", ErrNoGmail) }, want: ErrNoGmail},
		{name: "another mailbox", google: func(g *fakeGoogle) { g.mailbox = "blair@example.com" }, want: errProfileMailbox},
		{name: "email inbok cannot store", google: func(g *fakeGoogle) {
			g.grant.Identity.Email = "Alex <alex@example.com>"
			g.mailbox = g.grant.Identity.Email
		}, want: errGoogleEmail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newConnectTest(t)
			if tc.google != nil {
				tc.google(c.google)
			}
			callback := tc.callback
			if callback.Error == "" && tc.want != errNoCode {
				callback.Code = testCode
			}
			callback.State = c.start(t, 0)

			_, err := c.connections.Finish(t.Context(), member, c.session, callback)

			require.ErrorIs(t, err, tc.want)
			assert.Empty(t, c.storage.accounts)
			assert.Empty(t, c.storage.states)
		})
	}
}

// The mailbox check compares emails as emailaddress.Parse spells them.
func TestFinishMailboxCase(t *testing.T) {
	c := newConnectTest(t)
	c.google.mailbox = "Alex@Example.com"
	state := c.start(t, 0)

	_, err := c.finish(t, state)

	require.NoError(t, err)
}

// Printing a grant or a state shows no token or verifier.
func TestPrintingHidesSecrets(t *testing.T) {
	grant := alexGrant()
	state := ConnectState{UserID: member, CodeVerifier: "verifier-1"}

	saved, err := json.Marshal(grant)
	require.NoError(t, err)
	printed := fmt.Sprintf("%v %+v %#v %v %+v %#v %s", grant, grant, grant, state, state, state, saved)

	assert.NotContains(t, printed, "refresh-1")
	assert.NotContains(t, printed, "access-1")
	assert.NotContains(t, printed, "verifier-1")
}
