-- +goose Up
-- A send attempt is one message a member asked to send, counted by the
-- member's send limit. It is kept apart from outgoing_messages, whose rows go
-- with their account, so disconnecting an account does not reset the count.
-- Only the owner and the time are kept; a member's rows older than the
-- limit's longest window are deleted with their next send.
CREATE TABLE send_attempts (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL -- from the API's clock, as the limit counts it
);

CREATE INDEX send_attempts_user_created_idx ON send_attempts (user_id, created_at);

-- +goose Down
DROP TABLE send_attempts;
