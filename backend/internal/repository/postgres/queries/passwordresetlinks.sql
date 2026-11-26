-- name: ReadPasswordResetLink :one
-- With the link's user, so a completion can refuse before it hashes.
SELECT l.user_id, l.email, l.created_at, l.expires_at,
    u.email AS user_email, u.role, (u.deactivated_at IS NOT NULL)::boolean AS deactivated
FROM password_reset_links l
JOIN users u ON u.id = l.user_id
WHERE l.token_hash = @token_hash;

-- name: LockPasswordResetUser :exec
-- The account lock is shared with sign-in, deactivation, and recovery, so a
-- completion and a deactivation each see the other's result.
SELECT id FROM users WHERE id = @user_id FOR UPDATE;

-- name: DeleteUserPasswordResetLinks :execrows
DELETE FROM password_reset_links WHERE user_id = @user_id;

-- name: LockPasswordResetAccount :one
-- The account lock serializes one account's requests, so the email limits
-- count every link, and orders a request with completions and deactivations.
SELECT id, email, role, (email_verified_at IS NOT NULL)::boolean AS verified,
    (deactivated_at IS NOT NULL)::boolean AS deactivated
FROM users WHERE id = @id FOR UPDATE;

-- name: InsertPasswordResetLink :exec
INSERT INTO password_reset_links (token_hash, user_id, email, created_at, expires_at)
VALUES (@token_hash, @user_id, @email, @created_at, @expires_at);

-- name: DeleteExpiredPasswordResetLinkBatch :execrows
-- The cleanup job deletes expired links, a batch at a time. Rows a request
-- or completion holds are left for a later batch instead of waited for.
DELETE FROM password_reset_links WHERE token_hash IN (
    SELECT expired.token_hash FROM password_reset_links expired
    WHERE expired.expires_at <= @expired_by
    LIMIT @batch_size
    FOR UPDATE SKIP LOCKED);

-- name: ReadPasswordChangedNotice :one
-- The notice reads the address when it runs, which can be long after the
-- reset. FOR SHARE waits for a deactivation that holds the row, so the
-- flag is read after it, before mail goes out.
SELECT email, (deactivated_at IS NULL)::boolean AS active FROM users WHERE id = @id
FOR SHARE;
