-- +goose Up
-- The public id names a session to its owner; token_hash never leaves the
-- server. It is random, so it reveals nothing about other sessions. Each
-- existing row gets its own value.
ALTER TABLE sessions
    ADD COLUMN id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE;

-- +goose Down
ALTER TABLE sessions DROP COLUMN id;
