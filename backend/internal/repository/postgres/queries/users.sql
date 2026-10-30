-- name: LockUsers :exec
-- Serialize setup claims and other user writes while allowing status reads.
LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE;

-- name: HasAdminUser :one
SELECT EXISTS (SELECT 1 FROM users WHERE role = 'admin');

-- name: InsertAdminUser :one
INSERT INTO users (email, password_hash, role)
VALUES (@email, @password_hash, 'admin')
RETURNING id, email, role;

-- name: ReadUserCredentials :one
SELECT id, email, role, password_hash FROM users
WHERE lower(email) = lower(@email);

-- name: ReadUserByID :one
SELECT id, email, role FROM users
WHERE id = @id;

-- name: LockSignInUser :one
-- Compare password proof with the current hash while recovery shares this lock.
SELECT password_hash FROM users WHERE id = @user_id FOR UPDATE;
