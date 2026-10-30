-- name: InsertSession :exec
INSERT INTO sessions (token_hash, user_id, created_at, last_used_at, expires_at)
VALUES (@token_hash, @user_id, @created_at, @last_used_at, @expires_at);

-- name: LockSession :one
-- Decisions in Go use the latest state after any previous update commits.
SELECT user_id, created_at, last_used_at, expires_at
FROM sessions WHERE token_hash = @token_hash FOR UPDATE;

-- name: TouchSession :exec
UPDATE sessions SET last_used_at = @last_used_at WHERE token_hash = @token_hash;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = @token_hash;

-- name: LockUserSession :one
SELECT user_id, created_at, last_used_at, expires_at
FROM sessions WHERE user_id = @user_id AND token_hash = @token_hash FOR UPDATE;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = @user_id;

-- name: ReadSessionIdentity :one
-- Recheck the session together with the account and factor used for access.
SELECT users.id, users.email, users.role,
    (user_totp.confirmed_at IS NOT NULL)::boolean AS totp_confirmed
FROM sessions
JOIN users ON users.id = sessions.user_id
LEFT JOIN user_totp ON user_totp.user_id = users.id
WHERE sessions.token_hash = @token_hash;
