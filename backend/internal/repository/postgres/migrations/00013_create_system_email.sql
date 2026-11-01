-- +goose Up
-- The instance's own sender, separate from members' mailboxes. The always-true
-- key allows one row. Field limits belong to the API contract; these checks
-- repeat only systememail's consistency rules, so a write that skips it still
-- cannot send credentials without TLS or store a username and password apart.
CREATE TABLE system_email (
    id                  boolean PRIMARY KEY DEFAULT true CHECK (id),
    host                text NOT NULL,
    port                integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    security            text NOT NULL CHECK (security IN ('tls', 'starttls', 'none')),
    username            text CHECK (username <> ''), -- NULL: no sign-in
    password_key_id     text CHECK (password_key_id <> ''),
    password_ciphertext bytea CHECK (octet_length(password_ciphertext) > 0),
    from_address        text NOT NULL,
    from_name           text NOT NULL,
    tested_at           timestamptz, -- NULL: the current settings have not passed a test
    revision            bigint NOT NULL DEFAULT 1, -- every save adds one; a test records only if unchanged
    CONSTRAINT system_email_password_sealed
        CHECK ((password_key_id IS NULL) = (password_ciphertext IS NULL)),
    CONSTRAINT system_email_sign_in_paired
        CHECK ((username IS NULL) = (password_key_id IS NULL)),
    CONSTRAINT system_email_sign_in_encrypted
        CHECK (security <> 'none' OR username IS NULL)
);

-- +goose Down
DROP TABLE system_email;
