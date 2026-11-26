-- name: LockAttemptClient :exec
-- LockAttemptClient takes a transaction-scoped advisory lock for one client.
-- It also covers the first attempt, when there is no row to lock yet.
SELECT pg_advisory_xact_lock(hashtextextended('amplop.sign-in:' || @client_prefix::text, 0));

-- name: DeleteOldSignInAttempts :exec
DELETE FROM sign_in_attempts WHERE created_at <= @cutoff;

-- name: InsertSignInAttempt :one
INSERT INTO sign_in_attempts (email, client_prefix, created_at)
VALUES (@email, @client_prefix, @created_at)
RETURNING id;

-- name: ListSignInAttempts :many
SELECT email, client_prefix, created_at FROM sign_in_attempts WHERE client_prefix = @client_prefix;

-- name: DeleteSignInAttempt :exec
DELETE FROM sign_in_attempts WHERE id = @id;

-- name: DeleteSignInAttemptsByEmail :exec
DELETE FROM sign_in_attempts WHERE email = @email;
