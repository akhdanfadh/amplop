-- name: LockRegistrationAttempts :exec
-- Serialize every registration request: the instance limit counts them all.
SELECT pg_advisory_xact_lock(hashtextextended('amplop.registration-attempt', 0));

-- name: DeleteOldRegistrationAttempts :exec
DELETE FROM registration_attempts WHERE created_at <= @since;

-- name: ListRegistrationAttempts :many
SELECT email, client_prefix, outcome, created_at FROM registration_attempts;

-- name: InsertRegistrationAttempt :exec
INSERT INTO registration_attempts (email, client_prefix, outcome, created_at)
VALUES (@email, @client_prefix, @outcome, @created_at);

-- name: DeleteAllRegistrationAttempts :exec
DELETE FROM registration_attempts;
