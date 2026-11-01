-- name: ReadMemberUsers :many
-- An unconfirmed enrollment is not a factor yet.
SELECT u.id, u.email, u.role, u.created_at, u.deactivated_at,
    (u.email_verified_at IS NOT NULL)::boolean AS email_verified,
    EXISTS (SELECT 1 FROM user_totp t WHERE t.user_id = u.id AND t.confirmed_at IS NOT NULL) AS totp_enabled
FROM users u
ORDER BY u.created_at, u.id;

-- name: LockMemberUser :one
-- The account lock is shared with sign-in, enrollment, and recovery, so
-- their writes for this account wait until a status change commits.
SELECT u.id, u.email, u.role, u.created_at, u.deactivated_at,
    (u.email_verified_at IS NOT NULL)::boolean AS email_verified,
    EXISTS (SELECT 1 FROM user_totp t WHERE t.user_id = u.id AND t.confirmed_at IS NOT NULL) AS totp_enabled
FROM users u
WHERE u.id = @user_id
FOR UPDATE OF u;

-- name: UpdateUserDeactivatedAt :one
UPDATE users SET deactivated_at = @deactivated_at WHERE id = @user_id
RETURNING deactivated_at;
