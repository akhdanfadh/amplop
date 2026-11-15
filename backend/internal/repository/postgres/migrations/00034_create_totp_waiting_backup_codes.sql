-- +goose Up
-- A backup-code set waiting for "I've saved these codes": the hashes that
-- become active and the hash of the one-time token that activates it. At
-- most one per factor.
--
-- For an enrollment's waiting set only, an encrypted copy of its codes to
-- show again. A replacement's set keeps no copy: its codes appear only once.
-- A copy is its key and its ciphertext together, or neither.
CREATE TABLE totp_waiting_backup_codes (
    user_id          bigint PRIMARY KEY REFERENCES user_totp (user_id) ON DELETE CASCADE,
    code_hashes      text[] NOT NULL CHECK (cardinality(code_hashes) > 0),
    token_hash       bytea NOT NULL CHECK (octet_length(token_hash) = 32),
    expires_at       timestamptz NOT NULL,
    key_id           text CHECK (key_id <> ''),
    codes_ciphertext bytea CHECK (octet_length(codes_ciphertext) > 0),
    CHECK ((key_id IS NULL) = (codes_ciphertext IS NULL))
);

-- +goose Down
DROP TABLE totp_waiting_backup_codes;
