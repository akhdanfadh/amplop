-- name: LockPasswordResetClient :exec
-- Serializes one client's requests, so the client limit counts every one.
-- It also covers the first request, when there is no row to lock yet.
SELECT pg_advisory_xact_lock(hashtextextended('inbok.password-reset:' || @client_prefix::text, 0));

-- name: DeleteOldPasswordResetAttempts :exec
-- A row counts toward a limit until both its times are at or before since.
DELETE FROM password_reset_attempts
WHERE created_at <= @since AND (linked_at IS NULL OR linked_at <= @since);

-- name: ListPasswordResetClientAttempts :many
SELECT created_at FROM password_reset_attempts WHERE client_prefix = @client_prefix;

-- name: InsertPasswordResetAttempt :one
INSERT INTO password_reset_attempts (client_prefix, created_at)
VALUES (@client_prefix, @created_at)
RETURNING id;

-- name: ListPasswordResetUserAttempts :many
-- The times links were saved for the user, within the longest window.
SELECT linked_at::timestamptz FROM password_reset_attempts
WHERE user_id = @user_id AND linked_at > @since;

-- name: UpdatePasswordResetAttemptLink :exec
UPDATE password_reset_attempts SET user_id = @user_id, linked_at = @linked_at WHERE id = @id;
