-- +goose Up
-- A password reset link lets whoever controls a user's verified email choose
-- a new password. Only the secret's SHA-256 hash is kept. Expiry is checked in
-- Go when a link is used.
CREATE TABLE password_reset_links (
    token_hash bytea PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email      text NOT NULL CHECK (email = lower(email COLLATE "C")), -- the user's email when the link was sent
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX password_reset_links_user_id_idx ON password_reset_links (user_id);
CREATE INDEX password_reset_links_expires_at_idx ON password_reset_links (expires_at);

-- +goose Down
DROP TABLE password_reset_links;
