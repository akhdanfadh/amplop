-- +goose Up
CREATE TABLE sign_in_attempts (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email       text,
    client_addr text NOT NULL,
    created_at  timestamptz NOT NULL
);

CREATE INDEX sign_in_attempts_client_addr_idx ON sign_in_attempts (client_addr);
CREATE INDEX sign_in_attempts_created_at_idx ON sign_in_attempts (created_at);

-- +goose Down
DROP TABLE sign_in_attempts;
