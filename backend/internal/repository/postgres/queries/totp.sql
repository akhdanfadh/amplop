-- name: LockTOTPUser :one
-- Lock the existing owner even before their first factor row exists.
SELECT id FROM users WHERE id = @user_id FOR UPDATE;

-- name: LockTOTPUserRole :one
-- Role checks must share the owner lock with factor deletion and promotion.
SELECT role FROM users WHERE id = @user_id FOR UPDATE;

-- name: ReadUserTOTP :one
SELECT key_id, secret_ciphertext, confirmed_at, last_step
FROM user_totp WHERE user_id = @user_id;

-- name: ReadUserTOTPStatus :one
-- One statement keeps the factor and count consistent during concurrent writes.
SELECT factor.confirmed_at,
    (SELECT count(*) FROM totp_backup_codes AS codes WHERE codes.user_id = @user_id) AS backup_codes_left
FROM user_totp AS factor WHERE factor.user_id = @user_id;

-- name: UpsertUserTOTP :exec
INSERT INTO user_totp (user_id, key_id, secret_ciphertext, confirmed_at, last_step)
VALUES (@user_id, @key_id, @secret_ciphertext, @confirmed_at, @last_step)
ON CONFLICT (user_id) DO UPDATE SET
    key_id = EXCLUDED.key_id,
    secret_ciphertext = EXCLUDED.secret_ciphertext,
    confirmed_at = EXCLUDED.confirmed_at,
    last_step = EXCLUDED.last_step;

-- name: DeleteUserTOTP :exec
DELETE FROM user_totp WHERE user_id = @user_id;
