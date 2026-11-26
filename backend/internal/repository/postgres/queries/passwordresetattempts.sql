-- name: LockPasswordResetClient :exec
-- Serializes one client's requests, so the client limit counts every one.
-- It also covers the first request, when there is no row to lock yet.
SELECT pg_advisory_xact_lock(hashtextextended('amplop.password-reset:' || @client_prefix::text, 0));

-- name: ListPasswordResetClientAttempts :many
-- The client's requests within the longest window; the hourly cleanup
-- deletes older ones.
SELECT created_at FROM password_reset_attempts
WHERE client_prefix = @client_prefix AND created_at > @since;

-- name: ReadPasswordResetUserID :one
-- The ID of the account with the email, if any. A request runs it for
-- every address, so its answer takes the same work either way.
SELECT id FROM users WHERE email = @email;

-- name: InsertPasswordResetAttempt :one
INSERT INTO password_reset_attempts (client_prefix, created_at)
VALUES (@client_prefix, @created_at)
RETURNING id;

-- name: ListPasswordResetUserAttempts :many
-- The times links were saved for the user, within the longest window.
SELECT linked_at::timestamptz FROM password_reset_attempts
WHERE user_id = @user_id AND linked_at > @since;

-- name: MarkPasswordResetAttemptLink :execrows
-- Marks the request's attempt with its first link. A later link for it, or
-- one whose attempt the cleanup already deleted, matches no row and gets
-- its own with InsertPasswordResetLinkAttempt.
UPDATE password_reset_attempts SET user_id = @user_id, linked_at = @linked_at
WHERE id = @id AND linked_at IS NULL;

-- name: InsertPasswordResetLinkAttempt :exec
-- Counts one more link for the account, with no client.
INSERT INTO password_reset_attempts (user_id, linked_at, created_at)
VALUES (@user_id, @linked_at, @linked_at);

-- name: DeleteOldPasswordResetAttemptBatch :execrows
-- The cleanup job deletes attempts no limit counts, a batch at a time: a
-- row counts toward a limit until both its times are at or before since.
-- Rows a request holds are left for a later batch instead of waited for.
DELETE FROM password_reset_attempts WHERE id IN (
    SELECT old.id FROM password_reset_attempts old
    WHERE old.created_at <= @since AND (old.linked_at IS NULL OR old.linked_at <= @since)
    LIMIT @batch_size
    FOR UPDATE SKIP LOCKED);
