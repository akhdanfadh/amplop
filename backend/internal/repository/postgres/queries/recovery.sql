-- name: LockRecoveryUser :one
-- The owner lock is shared with enrollment and sign-in writes.
SELECT id, email, role FROM users WHERE id = @user_id FOR UPDATE;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = @password_hash WHERE id = @user_id;

-- name: CountUserBackupCodes :one
SELECT count(*) FROM totp_backup_codes WHERE user_id = @user_id;

-- name: DeleteRecoveryUserTOTP :execrows
DELETE FROM user_totp WHERE user_id = @user_id;

-- name: DeleteRecoverySessions :execrows
DELETE FROM sessions WHERE user_id = @user_id;

-- name: DeleteUserSignInChallenges :execrows
DELETE FROM sign_in_challenges WHERE user_id = @user_id;

-- name: DeleteRecoveryTOTPAttempts :execrows
DELETE FROM totp_attempts WHERE user_id = @user_id;

-- name: DeleteRecoverySignInAttempts :execrows
DELETE FROM sign_in_attempts WHERE email = @email;

-- name: ReadRecoveryNotice :one
-- One recovery notice: the recipient's current email and whether they are
-- active, and the recovered account's email, NULL when it is gone. The two
-- IDs may be the same. The share lock waits for a deactivation in progress,
-- so the flag is read as ADR029 asks of a feature that sends mail.
SELECT recipient.email AS recipient_email,
       (recipient.deactivated_at IS NULL)::boolean AS recipient_active,
       recovered.email AS recovered_email
FROM users recipient
LEFT JOIN users recovered ON recovered.id = @recovered_id
WHERE recipient.id = @recipient_id
FOR SHARE OF recipient;

-- name: ListActiveAdministrators :many
-- Who a recovery is reported to, oldest account first.
SELECT id FROM users WHERE role = 'admin' AND deactivated_at IS NULL ORDER BY id;
