-- name: DeleteExpiredRegistrationLinks :exec
-- Rows a completion holds are left for later instead of waited for.
DELETE FROM registration_links WHERE token_hash IN (
    SELECT expired.token_hash FROM registration_links expired
    WHERE expired.expires_at <= @expired_by
    FOR UPDATE SKIP LOCKED);

-- name: InsertRegistrationLink :exec
INSERT INTO registration_links (token_hash, email, created_at, expires_at)
VALUES (@token_hash, @email, @created_at, @expires_at);

-- name: DeleteAllRegistrationLinks :exec
DELETE FROM registration_links;

-- name: ReadRegistrationLink :one
SELECT email, created_at, expires_at FROM registration_links
WHERE token_hash = @token_hash;

-- name: LockRegistrationLink :one
SELECT email, created_at, expires_at FROM registration_links
WHERE token_hash = @token_hash FOR UPDATE;

-- name: DeleteRegistrationLinksByEmail :exec
DELETE FROM registration_links WHERE email = @email;
