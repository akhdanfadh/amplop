-- name: LockAttemptAddress :exec
-- LockAttemptAddress takes a transaction-scoped advisory lock for one address.
-- It also covers the first attempt, when there is no row to lock yet.
SELECT pg_advisory_xact_lock(hashtextextended('inbok.sign-in:' || @client_addr::text, 0));

-- name: DeleteOldSignInAttempts :exec
DELETE FROM sign_in_attempts WHERE created_at <= @cutoff;

-- name: InsertSignInAttempt :one
INSERT INTO sign_in_attempts (email, client_addr, created_at)
VALUES (@email, @client_addr, @created_at)
RETURNING id;

-- name: ListSignInAttempts :many
SELECT email, client_addr, created_at FROM sign_in_attempts WHERE client_addr = @client_addr;

-- name: DeleteSignInAttempt :exec
DELETE FROM sign_in_attempts WHERE id = @id;

-- name: DeleteSignInAttemptsByEmail :exec
DELETE FROM sign_in_attempts WHERE email = @email;
