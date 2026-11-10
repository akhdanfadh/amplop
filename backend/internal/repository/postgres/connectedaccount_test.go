package postgres

import (
	"context"
	"errors"
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
	db          *DB
	conn        *pgx.Conn
	ring        *encryption.Keyring
	service     *connectedaccount.Service
	member      int64
	otherMember int64
}

// accountWorkflows saves each account removal job as a row of
// test_account_removal_jobs, and each sync job as a row of
// test_sync_jobs, in the caller's transaction, or fails with
// removalErr or syncErr.
type accountWorkflows struct {
	Workflows
	removalErr error
	syncErr    error
}

func (w accountWorkflows) EnqueueAccountRemoval(ctx context.Context, tx pgx.Tx, removalID int64) error {
	if w.removalErr != nil {
		return w.removalErr
	}
	_, err := tx.Exec(ctx, "INSERT INTO test_account_removal_jobs (removal_id) VALUES ($1)", removalID)
	return err
}

func (w accountWorkflows) EnqueueSync(ctx context.Context, tx pgx.Tx, accountID int64) error {
	if w.syncErr != nil {
		return w.syncErr
	}
	_, err := tx.Exec(ctx, "INSERT INTO test_sync_jobs (account_id) VALUES ($1)", accountID)
	return err
}

// syncJobs lists the account IDs of the saved sync jobs.
func (c connectedAccountsTest) syncJobs(t *testing.T) []int64 {
	t.Helper()
	rows, err := c.conn.Query(t.Context(), "SELECT account_id FROM test_sync_jobs ORDER BY account_id")
	require.NoError(t, err)
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	require.NoError(t, err)
	return ids
}

// syncStatus returns account accountID's sync status, or "" when it has
// no sync state.
func (c connectedAccountsTest) syncStatus(t *testing.T, accountID int64) string {
	t.Helper()
	var status string
	err := c.conn.QueryRow(t.Context(), "SELECT status FROM sync_states WHERE connected_account_id = $1", accountID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	require.NoError(t, err)
	return status
}

// removalJobs lists the removal IDs of the saved jobs.
func (c connectedAccountsTest) removalJobs(t *testing.T) []int64 {
	t.Helper()
	rows, err := c.conn.Query(t.Context(), "SELECT removal_id FROM test_account_removal_jobs ORDER BY removal_id")
	require.NoError(t, err)
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	require.NoError(t, err)
	return ids
}

func newConnectedAccountsTest(t *testing.T) connectedAccountsTest {
	t.Helper()
	db, conn := setupStorage(t)
	_, err := conn.Exec(t.Context(), "CREATE TABLE test_account_removal_jobs (removal_id bigint NOT NULL)")
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), "CREATE TABLE test_sync_jobs (account_id bigint NOT NULL)")
	require.NoError(t, err)
	db = db.WithWorkflows(accountWorkflows{})
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	return connectedAccountsTest{
		db:          db,
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
	token, err := c.ring.Open(keyID, ciphertext, refreshTokenOwner(userID, accountID))
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
	assert.Equal(t, []int64{first.ID}, c.syncJobs(t), "only the first connect saves a full sync")
}

// A connected account starts with its sync state waiting for the full
// sync saved with it.
func TestConnectedAccountSyncSaved(t *testing.T) {
	c := newConnectedAccountsTest(t)

	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")

	require.NoError(t, err)
	assert.Equal(t, "waiting", c.syncStatus(t, account.ID))
	assert.Equal(t, []int64{account.ID}, c.syncJobs(t))
}

// A full sync that cannot be saved rolls the connect back, so no account
// is left without one.
func TestConnectedAccountSyncFails(t *testing.T) {
	c := newConnectedAccountsTest(t)
	failed := errors.New("queue unavailable")

	_, err := connectedaccount.NewService(c.db.WithWorkflows(accountWorkflows{syncErr: failed}), c.ring).Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	_, plainErr := connectedaccount.NewService(&DB{pool: c.db.pool}, c.ring).Connect(t.Context(), c.member, alexIdentity, "refresh-1")

	require.ErrorIs(t, err, failed)
	require.ErrorIs(t, plainErr, errNoWorkflows)
	assert.Equal(t, int64(0), c.countConnectedAccounts(t))
	var states int
	require.NoError(t, c.conn.QueryRow(t.Context(), "SELECT count(*) FROM sync_states").Scan(&states))
	assert.Equal(t, 0, states)
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
			_, disconnectErr = c.service.Disconnect(t.Context(), c.member, account.ID)
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

// A disconnect deletes the account, keeps its token as a removal, and
// saves the job that revokes it, which needs only the removal's ID.
func TestConnectedAccountDisconnect(t *testing.T) {
	c := newConnectedAccountsTest(t)
	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)
	keyID, ciphertext := c.storedCiphertext(t, account.ID)

	disconnected, err := c.service.Disconnect(t.Context(), c.member, account.ID)
	require.NoError(t, err)

	assert.Equal(t, account, disconnected)
	assert.Equal(t, int64(0), c.countConnectedAccounts(t))
	jobs := c.removalJobs(t)
	require.Len(t, jobs, 1)
	removal, err := c.db.ReadAccountRemoval(t.Context(), jobs[0])
	require.NoError(t, err)
	assert.Equal(t, connectedaccount.Removal{
		UserID: c.member, AccountID: account.ID,
		Credential: connectedaccount.Credential{KeyID: keyID, Ciphertext: ciphertext},
	}, removal)

	t.Run("again", func(t *testing.T) {
		_, err := c.service.Disconnect(t.Context(), c.member, account.ID)

		require.ErrorIs(t, err, connectedaccount.ErrNotFound)
		assert.Len(t, c.removalJobs(t), 1)
	})
}

func TestConnectedAccountDisconnectOtherMembersAccount(t *testing.T) {
	c := newConnectedAccountsTest(t)
	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)

	_, err = c.service.Disconnect(t.Context(), c.otherMember, account.ID)

	require.ErrorIs(t, err, connectedaccount.ErrNotFound)
	assert.Equal(t, int64(1), c.countConnectedAccounts(t))
	assert.Empty(t, c.removalJobs(t))
}

// A disconnect whose job cannot be saved keeps the account and saves no
// removal.
func TestDisconnectRollback(t *testing.T) {
	c := newConnectedAccountsTest(t)
	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)
	failed := errors.New("enqueue failed")

	_, failedErr := connectedaccount.NewService(c.db.WithWorkflows(accountWorkflows{removalErr: failed}), c.ring).Disconnect(t.Context(), c.member, account.ID)
	_, plainErr := connectedaccount.NewService(&DB{pool: c.db.pool}, c.ring).Disconnect(t.Context(), c.member, account.ID)

	require.ErrorIs(t, failedErr, failed)
	require.ErrorIs(t, plainErr, errNoWorkflows)
	assert.Equal(t, int64(1), c.countConnectedAccounts(t))
	var removals int64
	require.NoError(t, c.conn.QueryRow(t.Context(), "SELECT count(*) FROM account_removals").Scan(&removals))
	assert.Equal(t, int64(0), removals)
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

// connectAlex connects alex for the member with refresh-1 and returns the
// account and its ciphertext.
func (c connectedAccountsTest) connectAlex(t *testing.T) (connectedaccount.Account, []byte) {
	t.Helper()
	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)
	_, ciphertext := c.storedCiphertext(t, account.ID)
	return account, ciphertext
}

func TestAccountAccess(t *testing.T) {
	t.Run("saved", func(t *testing.T) {
		c := newConnectedAccountsTest(t)
		account, ciphertext := c.connectAlex(t)

		state, err := c.db.ReadConnectedAccountAccess(t.Context(), account.ID)
		require.NoError(t, err)

		keyID, _ := c.storedCiphertext(t, account.ID)
		assert.Equal(t, connectedaccount.AccessState{
			UserID:     c.member,
			Credential: connectedaccount.Credential{KeyID: keyID, Ciphertext: ciphertext},
		}, state)
	})

	// Work stops for an account Google refused or a deactivated member's, and
	// the state says which.
	t.Run("stopped", func(t *testing.T) {
		c := newConnectedAccountsTest(t)
		account, _ := c.connectAlex(t)
		_, err := c.conn.Exec(t.Context(), "UPDATE connected_accounts SET needs_reconnect_since = now() WHERE id = $1", account.ID)
		require.NoError(t, err)
		_, err = c.conn.Exec(t.Context(), "UPDATE users SET deactivated_at = now() WHERE id = $1", c.member)
		require.NoError(t, err)

		state, err := c.db.ReadConnectedAccountAccess(t.Context(), account.ID)
		require.NoError(t, err)

		assert.True(t, state.NeedsReconnect)
		assert.True(t, state.Deactivated)
	})

	t.Run("gone", func(t *testing.T) {
		c := newConnectedAccountsTest(t)

		_, err := c.db.ReadConnectedAccountAccess(t.Context(), 1)

		require.ErrorIs(t, err, connectedaccount.ErrNotFound)
	})

	// A deactivation in progress holds the user lock; the read waits for it
	// and sees the member deactivated, as ADR029 asks before provider work.
	t.Run("deactivating", func(t *testing.T) {
		c := newConnectedAccountsTest(t)
		account, _ := c.connectAlex(t)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		tx, err := c.conn.Begin(ctx)
		require.NoError(t, err)
		defer tx.Rollback(t.Context())
		_, err = tx.Exec(ctx, "SELECT id FROM users WHERE id = $1 FOR UPDATE", c.member)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, "UPDATE users SET deactivated_at = now() WHERE id = $1", c.member)
		require.NoError(t, err)
		type result struct {
			state connectedaccount.AccessState
			err   error
		}
		done := make(chan result, 1)
		go func() {
			state, err := c.db.ReadConnectedAccountAccess(ctx, account.ID)
			done <- result{state, err}
		}()

		require.Eventually(t, func() bool {
			var blocked bool
			err := c.db.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%ReadConnectedAccountAccess%')").Scan(&blocked)
			return err == nil && blocked
		}, 3*time.Second, 10*time.Millisecond)
		require.NoError(t, tx.Commit(ctx))
		read := <-done

		require.NoError(t, read.err)
		assert.True(t, read.state.Deactivated)
	})
}

func TestTokenReplace(t *testing.T) {
	t.Run("current", func(t *testing.T) {
		c := newConnectedAccountsTest(t)
		account, current := c.connectAlex(t)

		err := c.db.ReplaceConnectedAccountToken(t.Context(), account.ID, current, c.seal(t, c.member, account.ID, "refresh-2"))
		require.NoError(t, err)

		assert.Equal(t, "refresh-2", c.storedToken(t, c.member, account.ID))
	})

	// Google accepted the token the replacement comes from, so a refusal
	// another job recorded against that same token meanwhile is cleared.
	t.Run("marked meanwhile", func(t *testing.T) {
		c := newConnectedAccountsTest(t)
		account, current := c.connectAlex(t)
		require.NoError(t, c.db.MarkConnectedAccountReconnect(t.Context(), account.ID, current, time.Now()))

		err := c.db.ReplaceConnectedAccountToken(t.Context(), account.ID, current, c.seal(t, c.member, account.ID, "refresh-2"))
		require.NoError(t, err)

		accounts, err := c.service.List(t.Context(), c.member)
		require.NoError(t, err)
		assert.Nil(t, accounts[0].NeedsReconnectSince)
	})

	// A replacement for a token that is no longer saved, e.g. after a
	// reconnect, keeps the saved one.
	t.Run("changed", func(t *testing.T) {
		c := newConnectedAccountsTest(t)
		account, read := c.connectAlex(t)
		_, err := c.service.Reconnect(t.Context(), c.member, account.ID, alexIdentity, "refresh-3")
		require.NoError(t, err)

		err = c.db.ReplaceConnectedAccountToken(t.Context(), account.ID, read, c.seal(t, c.member, account.ID, "refresh-2"))

		require.ErrorIs(t, err, connectedaccount.ErrCredentialChanged)
		assert.Equal(t, "refresh-3", c.storedToken(t, c.member, account.ID))
	})

	t.Run("gone", func(t *testing.T) {
		c := newConnectedAccountsTest(t)
		account, read := c.connectAlex(t)
		_, err := c.service.Disconnect(t.Context(), c.member, account.ID)
		require.NoError(t, err)

		err = c.db.ReplaceConnectedAccountToken(t.Context(), account.ID, read, c.seal(t, c.member, account.ID, "refresh-2"))

		require.ErrorIs(t, err, connectedaccount.ErrCredentialChanged)
		assert.Equal(t, int64(0), c.countConnectedAccounts(t))
	})
}

func TestReconnectMark(t *testing.T) {
	// The first refusal's time stays, and a reconnect clears it.
	t.Run("current", func(t *testing.T) {
		c := newConnectedAccountsTest(t)
		account, current := c.connectAlex(t)
		first := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

		require.NoError(t, c.db.MarkConnectedAccountReconnect(t.Context(), account.ID, current, first))
		require.NoError(t, c.db.MarkConnectedAccountReconnect(t.Context(), account.ID, current, first.Add(time.Hour)))

		accounts, err := c.service.List(t.Context(), c.member)
		require.NoError(t, err)
		require.NotNil(t, accounts[0].NeedsReconnectSince)
		assert.Equal(t, first, *accounts[0].NeedsReconnectSince)
		reconnected, err := c.service.Reconnect(t.Context(), c.member, account.ID, alexIdentity, "refresh-2")
		require.NoError(t, err)
		assert.Nil(t, reconnected.NeedsReconnectSince)
	})

	// A refusal of a token that is no longer saved, e.g. after a reconnect,
	// marks nothing.
	t.Run("changed", func(t *testing.T) {
		c := newConnectedAccountsTest(t)
		account, read := c.connectAlex(t)
		_, err := c.service.Reconnect(t.Context(), c.member, account.ID, alexIdentity, "refresh-2")
		require.NoError(t, err)

		err = c.db.MarkConnectedAccountReconnect(t.Context(), account.ID, read, time.Now())

		require.ErrorIs(t, err, connectedaccount.ErrCredentialChanged)
		accounts, err := c.service.List(t.Context(), c.member)
		require.NoError(t, err)
		assert.Nil(t, accounts[0].NeedsReconnectSince)
	})
}

// seal returns refreshToken sealed for userID's account accountID, as the
// connectedaccount package seals it.
func (c connectedAccountsTest) seal(t *testing.T, userID, accountID int64, refreshToken string) connectedaccount.Credential {
	t.Helper()
	keyID, ciphertext, err := c.ring.Seal([]byte(refreshToken), refreshTokenOwner(userID, accountID))
	require.NoError(t, err)
	return connectedaccount.Credential{KeyID: keyID, Ciphertext: ciphertext}
}

// refreshTokenOwner returns the owner ADR006 binds userID's account
// accountID's refresh token to.
func refreshTokenOwner(userID, accountID int64) string {
	return "connected-account:" + strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(accountID, 10) + ":google-refresh-token"
}

// keepRemoval deletes account accountID and keeps its token as a removal,
// as a disconnect leaves it, and returns the removal's ID.
func (c connectedAccountsTest) keepRemoval(t *testing.T, accountID int64) int64 {
	t.Helper()
	var id int64
	require.NoError(t, c.conn.QueryRow(t.Context(), `INSERT INTO account_removals
		(user_id, account_id, provider, oauth_subject, refresh_token_key_id, refresh_token_ciphertext)
		SELECT user_id, id, provider, oauth_subject, refresh_token_key_id, refresh_token_ciphertext
		FROM connected_accounts WHERE id = $1 RETURNING id`, accountID).Scan(&id))
	_, err := c.conn.Exec(t.Context(), "DELETE FROM connected_accounts WHERE id = $1", accountID)
	require.NoError(t, err)
	return id
}

// fakeRevoker records the tokens it is asked to revoke.
type fakeRevoker struct{ revoked []string }

func (f *fakeRevoker) Revoke(_ context.Context, refreshToken string) error {
	f.revoked = append(f.revoked, refreshToken)
	return nil
}

// A removal reads back its token and whether another member connected the
// same Google account, and deleting it twice is no error.
func TestAccountRemovalStorage(t *testing.T) {
	c := newConnectedAccountsTest(t)
	shared, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)
	_, err = c.service.Connect(t.Context(), c.otherMember, alexIdentity, "refresh-2")
	require.NoError(t, err)
	alone, err := c.service.Connect(t.Context(), c.member, blairIdentity, "refresh-3")
	require.NoError(t, err)
	keyID, ciphertext := c.storedCiphertext(t, alone.ID)
	sharedID, aloneID := c.keepRemoval(t, shared.ID), c.keepRemoval(t, alone.ID)

	sharedRemoval, err := c.db.ReadAccountRemoval(t.Context(), sharedID)
	require.NoError(t, err)
	aloneRemoval, err := c.db.ReadAccountRemoval(t.Context(), aloneID)
	require.NoError(t, err)

	assert.True(t, sharedRemoval.Shared)
	assert.Equal(t, connectedaccount.Removal{
		UserID: c.member, AccountID: alone.ID,
		Credential: connectedaccount.Credential{KeyID: keyID, Ciphertext: ciphertext},
	}, aloneRemoval)
	for range 2 {
		require.NoError(t, c.db.DeleteAccountRemoval(t.Context(), aloneID))
	}
	_, err = c.db.ReadAccountRemoval(t.Context(), aloneID)
	require.ErrorIs(t, err, connectedaccount.ErrNotFound)
}

// A kept token opens with the owner it was sealed for, and is revoked at
// Google only when no other connection is the same Google account.
func TestAccountRemoval(t *testing.T) {
	c := newConnectedAccountsTest(t)
	// Member and other member both connect alex; member also connects blair.
	shared, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)
	_, err = c.service.Connect(t.Context(), c.otherMember, alexIdentity, "refresh-2")
	require.NoError(t, err)
	alone, err := c.service.Connect(t.Context(), c.member, blairIdentity, "refresh-3")
	require.NoError(t, err)
	sharedID, aloneID := c.keepRemoval(t, shared.ID), c.keepRemoval(t, alone.ID)
	google := &fakeRevoker{}
	removals := connectedaccount.NewRemovals(c.db, c.ring, google)

	sharedOutcome, err := removals.Revoke(t.Context(), sharedID)
	require.NoError(t, err)
	aloneOutcome, err := removals.Revoke(t.Context(), aloneID)
	require.NoError(t, err)

	assert.Equal(t, connectedaccount.RevokeShared, sharedOutcome)
	assert.Equal(t, connectedaccount.RevokeRevoked, aloneOutcome)
	assert.Equal(t, []string{"refresh-3"}, google.revoked)
	_, err = c.db.ReadAccountRemoval(t.Context(), aloneID)
	require.ErrorIs(t, err, connectedaccount.ErrNotFound)
}
