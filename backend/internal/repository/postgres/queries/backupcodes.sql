-- name: InsertTOTPBackupCode :exec
INSERT INTO totp_backup_codes (user_id, code_hash)
VALUES (@user_id, @code_hash);

-- name: ReadTOTPBackupCodes :many
SELECT id, code_hash FROM totp_backup_codes
WHERE user_id = @user_id ORDER BY id;

-- name: ConsumeTOTPBackupCode :one
-- Consume only the matched owner's row, in the protected action's transaction.
DELETE FROM totp_backup_codes
WHERE user_id = @user_id AND id = @id
RETURNING id;

-- name: DeleteTOTPBackupCodes :exec
DELETE FROM totp_backup_codes WHERE user_id = @user_id;
