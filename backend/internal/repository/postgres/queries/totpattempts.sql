-- name: DeleteOldTOTPAttempts :exec
-- Cleanup stays under the same user's lock as reservation and counting.
DELETE FROM totp_attempts WHERE user_id = @user_id AND created_at <= @cutoff;

-- name: InsertTOTPAttempt :one
INSERT INTO totp_attempts (user_id, created_at)
VALUES (@user_id, @created_at)
RETURNING id;

-- name: ListTOTPAttempts :many
SELECT user_id, created_at FROM totp_attempts WHERE user_id = @user_id;

-- name: DeleteTOTPAttempt :exec
DELETE FROM totp_attempts WHERE id = @id;
