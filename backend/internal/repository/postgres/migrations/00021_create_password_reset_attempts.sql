-- +goose Up
-- Accepted password reset requests, counted toward the limits and kept for
-- the longest window. A request records only its client before it is
-- answered; user_id and linked_at are set once a link is saved for that
-- account, so the email asked for is never kept for an address without one.
-- The client limit counts created_at; the account limits count linked_at,
-- which can come later.
CREATE TABLE password_reset_attempts (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    client_prefix text NOT NULL, -- an IPv4 address or an IPv6 /64, from clientprefix.Of
    user_id       bigint REFERENCES users (id) ON DELETE SET NULL, -- NULL: no link was saved, or its user is gone
    linked_at     timestamptz, -- when the link was saved; NULL: none was
    created_at    timestamptz NOT NULL
);

CREATE INDEX password_reset_attempts_client_prefix_idx ON password_reset_attempts (client_prefix, created_at);
CREATE INDEX password_reset_attempts_user_id_idx ON password_reset_attempts (user_id, linked_at) WHERE user_id IS NOT NULL;
CREATE INDEX password_reset_attempts_created_at_idx ON password_reset_attempts (created_at);

-- +goose Down
DROP TABLE password_reset_attempts;
