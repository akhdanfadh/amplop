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
