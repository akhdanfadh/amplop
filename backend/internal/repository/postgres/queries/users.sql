-- name: LockUsers :exec
-- Serialize setup claims and other user writes while allowing status reads.
LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE;

-- name: HasAdminUser :one
SELECT EXISTS (SELECT 1 FROM users WHERE role = 'admin');

-- name: InsertAdminUser :one
INSERT INTO users (email, password_hash, role)
VALUES (@email, @password_hash, 'admin')
RETURNING id, email;
