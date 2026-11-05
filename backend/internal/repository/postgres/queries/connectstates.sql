-- name: InsertConnectState :exec
INSERT INTO connect_states (state_hash, user_id, session_id, account_id,
    code_verifier, expires_at)
VALUES (@state_hash, @user_id, @session_id, @account_id,
    @code_verifier, @expires_at);

-- name: DeleteConnectState :one
-- Taking the row deletes it, so a state works once whatever happens next.
DELETE FROM connect_states WHERE state_hash = @state_hash
RETURNING user_id, session_id, account_id, code_verifier, expires_at;
