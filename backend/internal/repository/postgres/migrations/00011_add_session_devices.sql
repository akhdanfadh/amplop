-- +goose Up
-- Device details of the request that created each session. Existing rows
-- keep none, so they list as an unknown browser.
ALTER TABLE sessions
    ADD COLUMN user_agent text CHECK (user_agent <> ''), -- NULL: no header
    ADD COLUMN signed_in_from inet;

-- +goose Down
ALTER TABLE sessions
    DROP COLUMN signed_in_from,
    DROP COLUMN user_agent;
