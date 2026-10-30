-- +goose Up
CREATE TABLE audit_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    action      text NOT NULL,
    user_id     bigint NOT NULL REFERENCES users (id)
);

-- +goose Down
DROP TABLE audit_events;
