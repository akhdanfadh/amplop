-- +goose Up
CREATE TABLE sign_in_challenges (
    token_hash bytea PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    failures   integer NOT NULL DEFAULT 0 CHECK (failures >= 0)
);

-- New password proof can prune only this user's expired challenges.
CREATE INDEX sign_in_challenges_user_expiry_idx
    ON sign_in_challenges (user_id, expires_at);

-- +goose Down
DROP TABLE sign_in_challenges;
