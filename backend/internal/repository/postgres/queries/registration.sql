-- name: ReadRegistration :one
-- Registration needs system email settings that passed a test; none saved
-- counts as untested.
SELECT open,
    COALESCE((SELECT tested_at IS NOT NULL FROM system_email), false)::boolean AS system_email_tested
FROM registration;

-- name: LockRegistration :one
-- Serialize writers.
SELECT open FROM registration FOR UPDATE;

-- name: LockSystemEmailTested :one
-- Run after LockRegistration, as its own statement: one statement's subquery
-- keeps the tested time from before a lock wait. Holding the row until commit
-- makes a system email save or test wait, so the answer stays true.
SELECT (tested_at IS NOT NULL)::boolean AS tested FROM system_email FOR SHARE;

-- name: UpdateRegistration :exec
UPDATE registration SET open = @open;
