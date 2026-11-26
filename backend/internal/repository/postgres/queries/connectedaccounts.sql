-- name: NextConnectedAccountID :one
-- The id a new account's sealed token is bound to before its row exists.
SELECT nextval(pg_get_serial_sequence('connected_accounts', 'id'));

-- name: LockAccountColors :exec
-- Holds the user's account colours until the transaction ends, so two
-- connects of one user take colours one after the other.
SELECT pg_advisory_xact_lock(hashtextextended('amplop.account-color:' || (@user_id::bigint)::text, 0));

-- name: InsertConnectedAccount :one
-- Inserts nothing, and returns no row, when the user already has the
-- provider's subject. The account takes the lowest of the eight colours
-- the user's other accounts use least.
INSERT INTO connected_accounts (id, user_id, provider, email, oauth_subject,
    refresh_token_key_id, refresh_token_ciphertext, color)
OVERRIDING SYSTEM VALUE
VALUES (@id, @user_id, @provider, @email, @oauth_subject,
    @refresh_token_key_id, @refresh_token_ciphertext, (
        SELECT slot::smallint FROM generate_series(1, 8) AS slot
        ORDER BY (SELECT count(*) FROM connected_accounts other
            WHERE other.user_id = @user_id AND other.color = slot), slot
        LIMIT 1
    ))
ON CONFLICT (user_id, provider, oauth_subject) DO NOTHING
RETURNING id, user_id, provider, email, oauth_subject, needs_reconnect_since, created_at, name, color;

-- name: LockConnectedAccount :one
SELECT id, user_id, provider, email, oauth_subject, needs_reconnect_since, created_at, name, color
FROM connected_accounts
WHERE id = @id AND user_id = @user_id
-- No key column changes, so foreign key checks on the row need not wait.
FOR NO KEY UPDATE;

-- name: UpdateConnectedAccount :one
-- A new token clears needs_reconnect_since: Google has just accepted it.
UPDATE connected_accounts SET
    email = @email,
    refresh_token_key_id = @refresh_token_key_id,
    refresh_token_ciphertext = @refresh_token_ciphertext,
    needs_reconnect_since = NULL
WHERE id = @id AND user_id = @user_id
RETURNING id, user_id, provider, email, oauth_subject, needs_reconnect_since, created_at, name, color;

-- name: ListConnectedAccounts :many
-- With the credential, so the service can tell whether a key still opens it.
SELECT id, user_id, provider, email, oauth_subject, needs_reconnect_since, created_at, name, color,
    refresh_token_key_id, refresh_token_ciphertext
FROM connected_accounts
WHERE user_id = @user_id
ORDER BY created_at, id;

-- name: RenameConnectedAccount :one
-- An empty name clears it. Returns the credential too, so the service can
-- tell whether a key still opens it.
UPDATE connected_accounts SET name = nullif(@name::text, '')
WHERE id = @id AND user_id = @user_id
RETURNING id, user_id, provider, email, oauth_subject, needs_reconnect_since, created_at, name, color,
    refresh_token_key_id, refresh_token_ciphertext;

-- name: DeleteConnectedAccount :one
-- Returns the credential too, which the disconnect keeps for revocation.
DELETE FROM connected_accounts WHERE id = @id AND user_id = @user_id
RETURNING id, user_id, provider, email, oauth_subject, needs_reconnect_since, created_at, name, color,
    refresh_token_key_id, refresh_token_ciphertext;

-- name: InsertAccountRemoval :one
INSERT INTO account_removals (user_id, account_id, provider, oauth_subject,
    refresh_token_key_id, refresh_token_ciphertext)
VALUES (@user_id, @account_id, @provider, @oauth_subject,
    @refresh_token_key_id, @refresh_token_ciphertext)
RETURNING id;

-- name: ReadAccountRemoval :one
-- shared is whether a connected account is the same provider's account,
-- whose grant revoking would end too.
SELECT r.user_id, r.account_id, r.refresh_token_key_id, r.refresh_token_ciphertext,
    EXISTS (
        SELECT 1 FROM connected_accounts ca
        WHERE ca.provider = r.provider AND ca.oauth_subject = r.oauth_subject
    ) AS shared
FROM account_removals r
WHERE r.id = @id;

-- name: DeleteAccountRemoval :exec
DELETE FROM account_removals WHERE id = @id;

-- name: ReadConnectedAccountAccess :one
-- The share lock waits for a deactivation in progress, so the member's flag
-- is read after it, before provider work.
SELECT ca.user_id, ca.refresh_token_key_id, ca.refresh_token_ciphertext,
    (ca.needs_reconnect_since IS NOT NULL)::boolean AS needs_reconnect,
    (u.deactivated_at IS NOT NULL)::boolean AS deactivated
FROM connected_accounts ca
JOIN users u ON u.id = ca.user_id
WHERE ca.id = @id
FOR SHARE OF u;

-- name: ReplaceConnectedAccountToken :execrows
-- Replaces only the token the caller read, so a reconnect or another job's
-- replacement saved meanwhile stays. Google accepted that token to send the
-- replacement, so a refusal another job recorded against it is cleared.
UPDATE connected_accounts SET
    refresh_token_key_id = @refresh_token_key_id,
    refresh_token_ciphertext = @refresh_token_ciphertext,
    needs_reconnect_since = NULL
WHERE id = @id AND refresh_token_ciphertext = @current_ciphertext;

-- name: MarkConnectedAccountReconnect :execrows
-- Marks only the token the caller had refused, and keeps the first time.
UPDATE connected_accounts SET
    needs_reconnect_since = coalesce(needs_reconnect_since, sqlc.arg(at)::timestamptz)
WHERE id = @id AND refresh_token_ciphertext = @current_ciphertext;
