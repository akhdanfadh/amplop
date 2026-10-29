-- +goose Up
-- Enrollment ends every old session for one user.
CREATE INDEX sessions_user_idx ON sessions (user_id);

-- +goose Down
DROP INDEX sessions_user_idx;
