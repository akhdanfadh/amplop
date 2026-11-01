-- name: LockSystemEmail :exec
-- Serialize settings writes, including the first, while allowing reads.
LOCK TABLE system_email IN SHARE ROW EXCLUSIVE MODE;

-- name: ReadSystemEmail :one
SELECT host, port, security, username, password_key_id, password_ciphertext,
    from_address, from_name, tested_at, revision
FROM system_email;

-- name: UpsertSystemEmail :exec
INSERT INTO system_email (host, port, security, username, password_key_id,
    password_ciphertext, from_address, from_name)
VALUES (@host, @port, @security, @username, @password_key_id,
    @password_ciphertext, @from_address, @from_name)
ON CONFLICT (id) DO UPDATE SET
    host = EXCLUDED.host,
    port = EXCLUDED.port,
    security = EXCLUDED.security,
    username = EXCLUDED.username,
    password_key_id = EXCLUDED.password_key_id,
    password_ciphertext = EXCLUDED.password_ciphertext,
    from_address = EXCLUDED.from_address,
    from_name = EXCLUDED.from_name,
    -- New settings have not passed a test, and a test of the old revision
    -- must not be recorded for them.
    tested_at = NULL,
    revision = system_email.revision + 1;

-- name: UpdateSystemEmailTestedAt :exec
UPDATE system_email SET tested_at = @tested_at
    -- A save since the read changed the revision, so this matches no row.
    WHERE revision = @revision;

-- name: HasSystemEmail :one
SELECT EXISTS (SELECT 1 FROM system_email);
