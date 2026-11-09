-- +goose Up
-- A connect state is one start of the Gmail connect flow (ADR006): the
-- callback accepts it once, from the same session, before it expires. Only
-- the state's SHA-256 hash is kept; the PKCE verifier is kept as is, since
-- the code exchange sends it to Google. Both times come from the API's clock,
-- and expiry is checked in Go, as for emailed links.
CREATE TABLE connect_states (
    state_hash    bytea PRIMARY KEY,
    user_id       bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    session_id    uuid NOT NULL REFERENCES sessions (id) ON DELETE CASCADE, -- signing out ends the flow
    account_id    bigint REFERENCES connected_accounts (id) ON DELETE CASCADE, -- NULL: a new account; else the one to reconnect
    code_verifier text NOT NULL CHECK (code_verifier <> ''),
    created_at    timestamptz NOT NULL, -- the start, which the member's limit counts
    expires_at    timestamptz NOT NULL
);

-- Also serves counting a member's unfinished starts.
CREATE INDEX connect_states_user_id_idx ON connect_states (user_id);
CREATE INDEX connect_states_session_id_idx ON connect_states (session_id);
CREATE INDEX connect_states_account_id_idx ON connect_states (account_id) WHERE account_id IS NOT NULL;
CREATE INDEX connect_states_expires_at_idx ON connect_states (expires_at);

-- +goose Down
DROP TABLE connect_states;
