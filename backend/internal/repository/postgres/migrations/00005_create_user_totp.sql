-- +goose Up
CREATE TABLE user_totp (
    user_id           bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    key_id            text NOT NULL CHECK (key_id <> ''),
    secret_ciphertext bytea NOT NULL CHECK (octet_length(secret_ciphertext) > 0),
    confirmed_at      timestamptz,
    last_step         bigint NOT NULL DEFAULT -1 CHECK (last_step >= -1)
);

-- +goose Down
DROP TABLE user_totp;
