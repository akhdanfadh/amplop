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
