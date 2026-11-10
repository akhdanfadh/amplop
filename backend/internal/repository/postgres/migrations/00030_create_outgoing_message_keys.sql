-- +goose Up
-- An outgoing message key is the Idempotency-Key of the request that saved
-- a message, so a repeated request returns that message instead of saving a
-- second (ADR006). A key belongs to the member who sent it: another member's
-- use of the same key is another request, and never seen. It describes the
-- request, not the message, so a message sent again, which no request named,
-- has none; and it goes with its message.
CREATE TABLE outgoing_message_keys (
    user_id         bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    idempotency_key uuid NOT NULL,
    message_id      uuid NOT NULL UNIQUE REFERENCES outgoing_messages (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, idempotency_key)
);

-- +goose Down
DROP TABLE outgoing_message_keys;
