-- name: InsertSignInChallenge :exec
INSERT INTO sign_in_challenges (token_hash, user_id, expires_at)
VALUES (@token_hash, @user_id, @expires_at);

-- name: ReadSignInChallengeUser :one
-- Locate the owner before locking the user, then recheck the owned challenge.
SELECT user_id FROM sign_in_challenges WHERE token_hash = @token_hash;

-- name: LockSignInChallenge :one
-- Lock the user first when the action also updates a factor or creates a session.
SELECT user_id, expires_at, failures FROM sign_in_challenges
WHERE user_id = @user_id AND token_hash = @token_hash FOR UPDATE;

-- name: UpdateSignInChallengeFailures :exec
UPDATE sign_in_challenges SET failures = @failures
WHERE user_id = @user_id AND token_hash = @token_hash;

-- name: DeleteSignInChallenge :one
-- Consume with the protected action; rollback leaves the challenge available.
DELETE FROM sign_in_challenges
WHERE user_id = @user_id AND token_hash = @token_hash
RETURNING token_hash;

-- name: DeleteExpiredSignInChallenges :exec
DELETE FROM sign_in_challenges
WHERE user_id = @user_id AND expires_at <= @cutoff;
