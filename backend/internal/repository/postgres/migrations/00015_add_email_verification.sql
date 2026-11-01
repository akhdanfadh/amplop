-- +goose Up
ALTER TABLE users ADD COLUMN email_verified_at timestamptz; -- NULL: the address is not verified

-- A link proves control of the address it was sent to. Only the secret's
-- SHA-256 hash is kept. Expiry is checked in Go when a link is used.
CREATE TABLE email_verification_links (
    token_hash bytea PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    address    text NOT NULL, -- the user's email when the link was sent
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX email_verification_links_user_created_idx ON email_verification_links (user_id, created_at);

-- +goose Down
DROP TABLE email_verification_links;
ALTER TABLE users DROP COLUMN email_verified_at;
