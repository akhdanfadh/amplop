-- +goose Up
-- A deactivated account keeps its row, history, and credentials, but cannot
-- sign in or use a session until an administrator reactivates it.
ALTER TABLE users ADD COLUMN deactivated_at timestamptz; -- NULL: the account is active

-- +goose Down
ALTER TABLE users DROP COLUMN deactivated_at;
