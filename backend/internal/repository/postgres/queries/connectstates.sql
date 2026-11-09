-- name: InsertConnectState :exec
INSERT INTO connect_states (state_hash, user_id, session_id, account_id,
    code_verifier, created_at, expires_at)
VALUES (@state_hash, @user_id, @session_id, @account_id,
    @code_verifier, @created_at, @expires_at);

-- name: DeleteConnectState :one
-- Taking the row deletes it, so a state works once whatever happens next.
DELETE FROM connect_states WHERE state_hash = @state_hash
RETURNING user_id, session_id, account_id, code_verifier, created_at, expires_at;

-- name: LockConnectStateUser :one
-- Serializes a member's starts, so the throttle counts every state, and
-- reads whether a deactivation committed first. No key column changes, so
-- rows referencing the user need not wait.
SELECT (deactivated_at IS NOT NULL)::boolean AS deactivated
FROM users WHERE id = @user_id FOR NO KEY UPDATE;

-- name: ListConnectStateTimes :many
SELECT created_at FROM connect_states WHERE user_id = @user_id;

-- name: DeleteExpiredConnectStateBatch :execrows
-- The cleanup job deletes every member's expired states, a batch at a time.
-- Rows a callback holds are left for a later batch instead of waited for.
DELETE FROM connect_states WHERE state_hash IN (
    SELECT expired.state_hash FROM connect_states expired
    WHERE expired.expires_at <= @expired_by
    LIMIT @batch_size
    FOR UPDATE SKIP LOCKED);
