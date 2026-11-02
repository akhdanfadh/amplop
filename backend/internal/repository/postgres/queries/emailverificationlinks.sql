-- name: LockEmailVerificationUser :one
-- Serializes a user's link requests and redeems, so the throttle counts every
-- link and a link verifies once, and reads whether a deactivation committed
-- first.
SELECT email, email_verified_at, (deactivated_at IS NOT NULL)::boolean AS deactivated
FROM users WHERE id = @user_id FOR UPDATE;

-- name: ListEmailVerificationLinkTimes :many
SELECT created_at FROM email_verification_links WHERE user_id = @user_id;

-- name: DeleteExpiredEmailVerificationLinks :exec
DELETE FROM email_verification_links WHERE user_id = @user_id AND expires_at <= @expired_by;

-- name: InsertEmailVerificationLink :exec
INSERT INTO email_verification_links (token_hash, user_id, address, created_at, expires_at)
VALUES (@token_hash, @user_id, @address, @created_at, @expires_at);

-- name: ReadEmailVerificationLink :one
SELECT user_id, address, created_at, expires_at FROM email_verification_links
WHERE token_hash = @token_hash;

-- name: UpdateUserEmailVerifiedAt :exec
UPDATE users SET email_verified_at = @email_verified_at WHERE id = @user_id;

-- name: DeleteUserEmailVerificationLinks :exec
DELETE FROM email_verification_links WHERE user_id = @user_id;

-- name: DeleteAllExpiredEmailVerificationLinks :exec
-- Registration requests clean up every user's expired links. Rows a user's
-- request or redeem holds are left for later instead of waited for, so the
-- two cannot deadlock.
DELETE FROM email_verification_links WHERE token_hash IN (
    SELECT expired.token_hash FROM email_verification_links expired
    WHERE expired.expires_at <= @expired_by
    FOR UPDATE SKIP LOCKED);
