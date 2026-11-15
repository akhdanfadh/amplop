-- name: InsertSession :exec
INSERT INTO sessions (token_hash, user_id, created_at, last_used_at, expires_at, user_agent, signed_in_from)
VALUES (@token_hash, @user_id, @created_at, @last_used_at, @expires_at, @user_agent, @signed_in_from);

-- name: LockSession :one
-- Decisions in Go use the latest state after any previous update commits.
SELECT token_hash, user_id, created_at, last_used_at, expires_at, user_agent, signed_in_from, id
FROM sessions WHERE token_hash = @token_hash FOR UPDATE;

-- name: TouchSession :exec
UPDATE sessions SET last_used_at = @last_used_at WHERE token_hash = @token_hash;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = @token_hash;

-- name: LockUserSession :one
SELECT token_hash, user_id, created_at, last_used_at, expires_at, user_agent, signed_in_from, id
FROM sessions WHERE user_id = @user_id AND token_hash = @token_hash FOR UPDATE;

-- name: LockUserSessionByID :one
-- The owner is part of the lookup, so another user's id finds nothing.
SELECT token_hash, user_id, created_at, last_used_at, expires_at, user_agent, signed_in_from, id
FROM sessions WHERE user_id = @user_id AND id = @id FOR UPDATE;

-- name: ReadUserSessions :many
-- Expiry is a Go rule, so expired rows are returned too. Random ids only
-- keep rows with equal sign-in times in one order.
SELECT token_hash, user_id, created_at, last_used_at, expires_at, user_agent, signed_in_from, id
FROM sessions WHERE user_id = @user_id
ORDER BY created_at DESC, id DESC;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = @user_id;

-- name: DeleteUserExpiredSessions :exec
-- Go supplies both limits, so the expiry rule stays in one place.
DELETE FROM sessions
WHERE user_id = @user_id AND (expires_at <= @expires_by OR last_used_at <= @last_used_by);

-- name: LockSessionUser :one
-- The owner lock comes first, as in finishing two-factor enrollment
-- (FinishTOTP), so deleting several of the user's sessions cannot deadlock
-- with it.
SELECT id FROM users WHERE id = @user_id FOR UPDATE;

-- name: DeleteUserOtherSessions :many
-- Returns the deleted rows so Go can count the ones that were still live.
DELETE FROM sessions WHERE user_id = @user_id AND token_hash <> @keep
RETURNING token_hash, user_id, created_at, last_used_at, expires_at, user_agent, signed_in_from, id;

-- name: ReadSessionIdentity :one
-- Recheck the session together with the account and factor used for access,
-- and read whether the account's email is verified. A deactivated account's
-- session identifies no one.
SELECT users.id, users.email, users.role,
    (user_totp.confirmed_at IS NOT NULL)::boolean AS totp_confirmed,
    (users.email_verified_at IS NOT NULL)::boolean AS email_verified
FROM sessions
JOIN users ON users.id = sessions.user_id
LEFT JOIN user_totp ON user_totp.user_id = users.id
WHERE sessions.token_hash = @token_hash AND users.deactivated_at IS NULL;
