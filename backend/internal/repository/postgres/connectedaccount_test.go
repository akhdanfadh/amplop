package postgres

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/encryption"
)

// connectedAccountsTest is a migrated database with two members and the
// connected account service on it.
type connectedAccountsTest struct {
	conn        *pgx.Conn
	ring        *encryption.Keyring
	service     *connectedaccount.Service
	member      int64
	otherMember int64
}

func newConnectedAccountsTest(t *testing.T) connectedAccountsTest {
	t.Helper()
	db, conn := setupStorage(t)
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	return connectedAccountsTest{
		conn:        conn,
		ring:        ring,
		service:     connectedaccount.NewService(db, ring),
		member:      insertConnectingMember(t, conn, "member@example.com"),
		otherMember: insertConnectingMember(t, conn, "other@example.com"),
	}
}

// insertConnectingMember adds a member with email and returns their ID.
func insertConnectingMember(t *testing.T, conn *pgx.Conn, email string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ($1, 'unused', 'member') RETURNING id", email).Scan(&id))
	return id
}

// storedCiphertext returns the sealed refresh token in account accountID's
// row and the ID of the key that sealed it.
func (c connectedAccountsTest) storedCiphertext(t *testing.T, accountID int64) (string, []byte) {
	t.Helper()
	var keyID string
	var ciphertext []byte
	require.NoError(t, c.conn.QueryRow(t.Context(),
		"SELECT refresh_token_key_id, refresh_token_ciphertext FROM connected_accounts WHERE id = $1", accountID).Scan(&keyID, &ciphertext))
	return keyID, ciphertext
}

// storedToken returns the refresh token in account accountID's row, opened
// with the owner ADR006 binds it to.
func (c connectedAccountsTest) storedToken(t *testing.T, userID, accountID int64) string {
	t.Helper()
	keyID, ciphertext := c.storedCiphertext(t, accountID)
	owner := "connected-account:" + strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(accountID, 10) + ":google-refresh-token"
	token, err := c.ring.Open(keyID, ciphertext, owner)
	require.NoError(t, err)
	return string(token)
}

// countConnectedAccounts returns how many accounts are saved for anyone.
func (c connectedAccountsTest) countConnectedAccounts(t *testing.T) int64 {
	t.Helper()
	var count int64
	require.NoError(t, c.conn.QueryRow(t.Context(), "SELECT count(*) FROM connected_accounts").Scan(&count))
	return count
}

var (
	alexIdentity  = connectedaccount.Identity{Subject: "100000000000000000001", Email: "alex@example.com"}
	blairIdentity = connectedaccount.Identity{Subject: "100000000000000000002", Email: "blair@example.com"}
)

func TestConnectedAccountStorage(t *testing.T) {
	c := newConnectedAccountsTest(t)

	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)

	assert.Equal(t, c.member, account.UserID)
	assert.Equal(t, connectedaccount.ProviderGmail, account.Provider)
	assert.Equal(t, "alex@example.com", account.Email)
	assert.Equal(t, alexIdentity.Subject, account.Subject)
	assert.Nil(t, account.NeedsReconnectSince)
	assert.WithinDuration(t, time.Now(), account.CreatedAt, time.Minute)
	_, ciphertext := c.storedCiphertext(t, account.ID)
	assert.NotContains(t, string(ciphertext), "refresh-1", "the token is stored sealed")
	assert.Equal(t, "refresh-1", c.storedToken(t, c.member, account.ID))
	accounts, err := c.service.List(t.Context(), c.member)
	require.NoError(t, err)
	assert.Equal(t, []connectedaccount.Account{account}, accounts)
}

func TestConnectedAccountConnectTwice(t *testing.T) {
	c := newConnectedAccountsTest(t)
	first, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)

	_, err = c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-2")

	require.ErrorIs(t, err, connectedaccount.ErrAlreadyConnected)
	assert.Equal(t, int64(1), c.countConnectedAccounts(t))
	assert.Equal(t, "refresh-1", c.storedToken(t, c.member, first.ID))
}

// Two members may each connect the same Google account, with their own
// token.
func TestConnectedAccountSameAccountTwoMembers(t *testing.T) {
	c := newConnectedAccountsTest(t)
	first, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)

	second, err := c.service.Connect(t.Context(), c.otherMember, alexIdentity, "refresh-2")
	require.NoError(t, err)

	assert.Equal(t, int64(2), c.countConnectedAccounts(t))
	assert.Equal(t, "refresh-1", c.storedToken(t, c.member, first.ID))
	assert.Equal(t, "refresh-2", c.storedToken(t, c.otherMember, second.ID))
}

// Connects of one Google account at once save it once; the others are
// told it is already connected.
func TestConnectedAccountConnectConcurrent(t *testing.T) {
	c := newConnectedAccountsTest(t)
	const attempts = 8

	var wg sync.WaitGroup
	errs := make([]error, attempts)
	for i := range attempts {
		wg.Go(func() {
			_, errs[i] = c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-"+strconv.Itoa(i))
		})
	}
	wg.Wait()

	saved := 0
	for _, err := range errs {
		if err == nil {
			saved++
			continue
		}
		require.ErrorIs(t, err, connectedaccount.ErrAlreadyConnected)
	}
	assert.Equal(t, 1, saved)
	assert.Equal(t, int64(1), c.countConnectedAccounts(t))
}

func TestConnectedAccountReconnect(t *testing.T) {
	// Given an account whose token Google refused.
	c := newConnectedAccountsTest(t)
	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)
	_, err = c.conn.Exec(t.Context(), "UPDATE connected_accounts SET needs_reconnect_since = now() WHERE id = $1", account.ID)
	require.NoError(t, err)

	// When the member reconnects it, and Google now reports another email.
	renamed := connectedaccount.Identity{Subject: alexIdentity.Subject, Email: "alex.rivera@example.com"}
	reconnected, err := c.service.Reconnect(t.Context(), c.member, account.ID, renamed, "refresh-2")
	require.NoError(t, err)

	// Then it is the same account, working again with the new token.
	assert.Equal(t, account.ID, reconnected.ID)
	assert.Equal(t, account.CreatedAt, reconnected.CreatedAt)
	assert.Equal(t, "alex.rivera@example.com", reconnected.Email)
	assert.Nil(t, reconnected.NeedsReconnectSince)
	assert.Equal(t, "refresh-2", c.storedToken(t, c.member, account.ID))
}

// Another Google account cannot take an account's place.
func TestConnectedAccountReconnectDifferentAccount(t *testing.T) {
	c := newConnectedAccountsTest(t)
	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)

	_, err = c.service.Reconnect(t.Context(), c.member, account.ID, blairIdentity, "refresh-2")

	require.ErrorIs(t, err, connectedaccount.ErrDifferentAccount)
	accounts, err := c.service.List(t.Context(), c.member)
	require.NoError(t, err)
	assert.Equal(t, []connectedaccount.Account{account}, accounts)
	assert.Equal(t, "refresh-1", c.storedToken(t, c.member, account.ID))
}

func TestConnectedAccountReconnectOtherMembersAccount(t *testing.T) {
	c := newConnectedAccountsTest(t)
	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)

	_, err = c.service.Reconnect(t.Context(), c.otherMember, account.ID, alexIdentity, "refresh-2")

	require.ErrorIs(t, err, connectedaccount.ErrNotFound)
	assert.Equal(t, "refresh-1", c.storedToken(t, c.member, account.ID))
}

// A reconnect and a disconnect of one account at once each either take
// effect or find the account gone; neither fails otherwise.
func TestConnectedAccountReconnectDisconnectConcurrent(t *testing.T) {
	c := newConnectedAccountsTest(t)
	for round := range 10 {
		account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-"+strconv.Itoa(round))
		require.NoError(t, err)

		var wg sync.WaitGroup
		var reconnectErr, disconnectErr error
		wg.Go(func() {
			_, reconnectErr = c.service.Reconnect(t.Context(), c.member, account.ID, alexIdentity, "refresh-new")
		})
		wg.Go(func() {
			disconnectErr = c.service.Disconnect(t.Context(), c.member, account.ID)
		})
		wg.Wait()

		if reconnectErr != nil {
			require.ErrorIs(t, reconnectErr, connectedaccount.ErrNotFound)
		}
		require.NoError(t, disconnectErr)
		assert.Equal(t, int64(0), c.countConnectedAccounts(t))
	}
}

func TestConnectedAccountList(t *testing.T) {
	c := newConnectedAccountsTest(t)
	first, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)
	_, err = c.service.Connect(t.Context(), c.otherMember, alexIdentity, "refresh-2")
	require.NoError(t, err)
	second, err := c.service.Connect(t.Context(), c.member, blairIdentity, "refresh-3")
	require.NoError(t, err)

	accounts, err := c.service.List(t.Context(), c.member)
	require.NoError(t, err)

	assert.Equal(t, []connectedaccount.Account{first, second}, accounts)
}

func TestConnectedAccountListEmpty(t *testing.T) {
	c := newConnectedAccountsTest(t)

	accounts, err := c.service.List(t.Context(), c.member)
	require.NoError(t, err)

	assert.NotNil(t, accounts)
	assert.Empty(t, accounts)
}

func TestConnectedAccountDisconnect(t *testing.T) {
	c := newConnectedAccountsTest(t)
	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)
	require.NoError(t, c.service.Disconnect(t.Context(), c.member, account.ID))

	err = c.service.Disconnect(t.Context(), c.member, account.ID)

	require.ErrorIs(t, err, connectedaccount.ErrNotFound)
	assert.Equal(t, int64(0), c.countConnectedAccounts(t))
}

func TestConnectedAccountDisconnectOtherMembersAccount(t *testing.T) {
	c := newConnectedAccountsTest(t)
	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)

	err = c.service.Disconnect(t.Context(), c.otherMember, account.ID)

	require.ErrorIs(t, err, connectedaccount.ErrNotFound)
	assert.Equal(t, int64(1), c.countConnectedAccounts(t))
}

// Deleting a member deletes their accounts and tokens with them.
func TestConnectedAccountUserDeleted(t *testing.T) {
	c := newConnectedAccountsTest(t)
	_, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)

	_, err = c.conn.Exec(t.Context(), "DELETE FROM users WHERE id = $1", c.member)
	require.NoError(t, err)

	assert.Equal(t, int64(0), c.countConnectedAccounts(t))
}

// The table refuses what connectedaccount never saves, so a write that
// skips it cannot either.
func TestConnectedAccountsChecks(t *testing.T) {
	c := newConnectedAccountsTest(t)
	const insert = `INSERT INTO connected_accounts (user_id, provider, email, oauth_subject, refresh_token_key_id, refresh_token_ciphertext)
		VALUES ($1, $2, $3, $4, '1', $5)`
	cases := []struct {
		name, provider, email, subject string
		ciphertext                     []byte
	}{
		{"unknown provider", "imap", "alex@example.com", "1", []byte{1}},
		{"uppercase ASCII in email", "gmail", "Alex@example.com", "1", []byte{1}},
		{"short email", "gmail", "a@", "1", []byte{1}},
		{"empty subject", "gmail", "alex@example.com", "", []byte{1}},
		{"empty ciphertext", "gmail", "alex@example.com", "1", []byte{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.conn.Exec(t.Context(), insert, c.member, tc.provider, tc.email, tc.subject, tc.ciphertext)

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Equal(t, checkViolation, pgErr.Code)
		})
	}
}
