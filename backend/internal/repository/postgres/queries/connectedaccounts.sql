-- name: NextConnectedAccountID :one
-- The id a new account's sealed token is bound to before its row exists.
SELECT nextval(pg_get_serial_sequence('connected_accounts', 'id'));

-- name: InsertConnectedAccount :one
-- Inserts nothing, and returns no row, when the user already has the
-- provider's subject.
INSERT INTO connected_accounts (id, user_id, provider, email, oauth_subject,
    refresh_token_key_id, refresh_token_ciphertext)
OVERRIDING SYSTEM VALUE
VALUES (@id, @user_id, @provider, @email, @oauth_subject,
    @refresh_token_key_id, @refresh_token_ciphertext)
ON CONFLICT (user_id, provider, oauth_subject) DO NOTHING
RETURNING id, user_id, provider, email, oauth_subject, needs_reconnect_since, created_at;

-- name: LockConnectedAccount :one
SELECT id, user_id, provider, email, oauth_subject, needs_reconnect_since, created_at
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
RETURNING id, user_id, provider, email, oauth_subject, needs_reconnect_since, created_at;

-- name: ListConnectedAccounts :many
SELECT id, user_id, provider, email, oauth_subject, needs_reconnect_since, created_at
FROM connected_accounts
WHERE user_id = @user_id
ORDER BY created_at, id;

-- name: DeleteConnectedAccount :execrows
DELETE FROM connected_accounts WHERE id = @id AND user_id = @user_id;

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
-- is read as ADR029 asks before provider work.
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
