-- name: LockSystemEmail :exec
-- Serialize settings writes, including the first, while allowing reads.
LOCK TABLE system_email IN SHARE ROW EXCLUSIVE MODE;

-- name: ReadSystemEmail :one
SELECT host, port, security, username, password_key_id, password_ciphertext,
    from_address, from_name, tested_at
FROM system_email;

-- name: UpsertSystemEmail :exec
INSERT INTO system_email (host, port, security, username, password_key_id,
    password_ciphertext, from_address, from_name, tested_at)
VALUES (@host, @port, @security, @username, @password_key_id,
    @password_ciphertext, @from_address, @from_name, @tested_at)
ON CONFLICT (id) DO UPDATE SET
    host = EXCLUDED.host,
    port = EXCLUDED.port,
    security = EXCLUDED.security,
    username = EXCLUDED.username,
    password_key_id = EXCLUDED.password_key_id,
    password_ciphertext = EXCLUDED.password_ciphertext,
    from_address = EXCLUDED.from_address,
    from_name = EXCLUDED.from_name,
    tested_at = EXCLUDED.tested_at;
