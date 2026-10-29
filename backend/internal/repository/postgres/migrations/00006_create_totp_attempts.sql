-- +goose Up
-- Keep only the owner and time; codes and network addresses are not stored.
CREATE TABLE totp_attempts (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL
);

CREATE INDEX totp_attempts_user_created_idx ON totp_attempts (user_id, created_at);

-- +goose Down
DROP TABLE totp_attempts;
