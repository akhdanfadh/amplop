-- +goose Up
-- Account preserves local-part case and lowercases the domain. Repeat its
-- length limit here so a write that misses validation cannot exceed it.
CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email         text NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
    password_hash text NOT NULL, -- Argon2id PHC string
    role          text NOT NULL CHECK (role IN ('admin', 'member')),
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Preserve the delivery address while rejecting case-only account duplicates.
CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email));

-- +goose Down
DROP TABLE users;
