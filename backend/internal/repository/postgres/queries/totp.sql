-- name: LockTOTPUser :one
-- Lock the existing owner even before their first factor row exists, and
-- read whether a deactivation committed first.
SELECT (deactivated_at IS NOT NULL)::boolean AS deactivated
FROM users WHERE id = @user_id FOR UPDATE;

-- name: LockTOTPUserRole :one
-- Role checks must share the owner lock with factor deletion and promotion;
-- also reads whether a deactivation committed first.
SELECT role, (deactivated_at IS NOT NULL)::boolean AS deactivated
FROM users WHERE id = @user_id FOR UPDATE;

-- name: ReadUserTOTP :one
SELECT key_id, secret_ciphertext, confirmed_at, last_step, started_at
FROM user_totp WHERE user_id = @user_id;

-- name: ReadUserTOTPStatus :one
-- One statement keeps the factor and count consistent during concurrent writes.
SELECT factor.confirmed_at, factor.started_at,
    (SELECT count(*) FROM totp_backup_codes AS codes WHERE codes.user_id = @user_id) AS backup_codes_left,
    waiting.expires_at AS waiting_expires_at
FROM user_totp AS factor
LEFT JOIN totp_waiting_backup_codes AS waiting ON waiting.user_id = factor.user_id
WHERE factor.user_id = @user_id;

-- name: UpsertUserTOTP :exec
INSERT INTO user_totp (user_id, key_id, secret_ciphertext, confirmed_at, last_step, started_at)
VALUES (@user_id, @key_id, @secret_ciphertext, @confirmed_at, @last_step, @started_at)
ON CONFLICT (user_id) DO UPDATE SET
    key_id = EXCLUDED.key_id,
    secret_ciphertext = EXCLUDED.secret_ciphertext,
    confirmed_at = EXCLUDED.confirmed_at,
    last_step = EXCLUDED.last_step,
    started_at = EXCLUDED.started_at;

-- name: DeleteUserTOTP :exec
DELETE FROM user_totp WHERE user_id = @user_id;

-- name: ReadTOTPWaitingBackupCodes :one
SELECT code_hashes, key_id, codes_ciphertext, token_hash, expires_at
FROM totp_waiting_backup_codes WHERE user_id = @user_id;

-- name: UpsertTOTPWaitingBackupCodes :exec
INSERT INTO totp_waiting_backup_codes (user_id, code_hashes, key_id, codes_ciphertext, token_hash, expires_at)
VALUES (@user_id, @code_hashes, @key_id, @codes_ciphertext, @token_hash, @expires_at)
ON CONFLICT (user_id) DO UPDATE SET
    code_hashes = EXCLUDED.code_hashes,
    key_id = EXCLUDED.key_id,
    codes_ciphertext = EXCLUDED.codes_ciphertext,
    token_hash = EXCLUDED.token_hash,
    expires_at = EXCLUDED.expires_at;

-- name: DeleteTOTPWaitingBackupCodes :exec
DELETE FROM totp_waiting_backup_codes WHERE user_id = @user_id;
