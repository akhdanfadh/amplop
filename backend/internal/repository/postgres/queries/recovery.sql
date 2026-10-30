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
