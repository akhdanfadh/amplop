-- +goose Up
-- A registration link lets whoever controls an email address choose the
-- password of a new account with it. No account exists until then, so a link
-- holds the email, not a user. Only the secret's SHA-256 hash is kept. Expiry is
-- checked in Go when a link is used.
CREATE TABLE registration_links (
    token_hash bytea PRIMARY KEY,
    email      text NOT NULL CHECK (email = lower(email COLLATE "C")),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX registration_links_email_idx ON registration_links (email);
CREATE INDEX registration_links_expires_at_idx ON registration_links (expires_at);

-- Accepted registration requests, counted toward the attempt limits and kept
-- for the longest window.
CREATE TABLE registration_attempts (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email       text NOT NULL CHECK (email = lower(email COLLATE "C")),
    client_addr text NOT NULL,
    outcome     text NOT NULL CHECK (outcome IN ('link', 'notice', 'skipped')),
    created_at  timestamptz NOT NULL
);

CREATE INDEX registration_attempts_created_at_idx ON registration_attempts (created_at);

-- Each registration request deletes every expired verification link.
CREATE INDEX email_verification_links_expires_at_idx ON email_verification_links (expires_at);

-- +goose Down
DROP INDEX email_verification_links_expires_at_idx;
DROP TABLE registration_attempts;
DROP TABLE registration_links;
