-- +goose Up
CREATE TABLE totp_backup_codes (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id   bigint NOT NULL REFERENCES user_totp (user_id) ON DELETE CASCADE,
    code_hash text NOT NULL CHECK (code_hash <> '')
);

CREATE INDEX totp_backup_codes_user_idx ON totp_backup_codes (user_id);

-- +goose Down
DROP TABLE totp_backup_codes;
