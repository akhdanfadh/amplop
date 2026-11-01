-- name: LockEmailVerificationUser :one
-- Serializes a user's link requests, so the throttle counts every link.
SELECT email, email_verified_at FROM users WHERE id = @user_id FOR UPDATE;

-- name: ListEmailVerificationLinkTimes :many
SELECT created_at FROM email_verification_links WHERE user_id = @user_id;

-- name: DeleteExpiredEmailVerificationLinks :exec
DELETE FROM email_verification_links WHERE user_id = @user_id AND expires_at <= @expired_by;

-- name: InsertEmailVerificationLink :exec
INSERT INTO email_verification_links (token_hash, user_id, address, created_at, expires_at)
VALUES (@token_hash, @user_id, @address, @created_at, @expires_at);
