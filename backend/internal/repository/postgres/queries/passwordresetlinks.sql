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
