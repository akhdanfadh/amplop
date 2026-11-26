-- +goose Up
-- An account removal is what is left to do for a disconnected account once
-- its row is gone: so far, revoking its refresh token at Google, which a job
-- does. The disconnect moves the token here in its transaction, so the job's
-- input holds only this row's id, never the token. user_id and account_id are
-- the deleted account's, which the sealed token is bound to. They are not
-- foreign keys: the account is gone, and the token can still be revoked if
-- its user is deleted before the job runs.
CREATE TABLE account_removals (
    id                       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id                  bigint NOT NULL,
    account_id               bigint NOT NULL,
    provider                 text NOT NULL CHECK (provider IN ('gmail')), -- as connected_accounts
    oauth_subject            text NOT NULL CHECK (oauth_subject <> ''), -- finds other connections of the same account
    refresh_token_key_id     text NOT NULL CHECK (refresh_token_key_id <> ''),
    refresh_token_ciphertext bytea NOT NULL CHECK (octet_length(refresh_token_ciphertext) > 0)
);

-- The job looks up whether another member connected the same account before
-- revoking; the unique index leads with user_id, so it cannot serve that.
CREATE INDEX connected_accounts_provider_subject_idx ON connected_accounts (provider, oauth_subject);

-- +goose Down
DROP INDEX connected_accounts_provider_subject_idx;
DROP TABLE account_removals;
