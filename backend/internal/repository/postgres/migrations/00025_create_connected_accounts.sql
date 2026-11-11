-- +goose Up
-- Email accounts members connect (ADR006), so far only Gmail. Each belongs to
-- one user and holds only the refresh token, sealed by connectedaccount and
-- bound to the user and the row's id, so storage hands out an id before the
-- insert.
CREATE TABLE connected_accounts (
    id                       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id                  bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider                 text NOT NULL CHECK (provider IN ('gmail')), -- each new provider widens the list
    email                    text NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254), -- from emailaddress.Parse
    oauth_subject            text NOT NULL CHECK (oauth_subject <> ''), -- the provider's sub, the account's lasting ID
    refresh_token_key_id     text NOT NULL CHECK (refresh_token_key_id <> ''),
    refresh_token_ciphertext bytea NOT NULL CHECK (octet_length(refresh_token_ciphertext) > 0),
    needs_reconnect_since    timestamptz, -- NULL: the provider accepted the token when last used
    created_at               timestamptz NOT NULL DEFAULT now(),
    -- What the member calls the account, shown instead of its address; NULL
    -- until they choose one.
    name                     text CHECK (name <> '' AND char_length(name) <= 64),
    -- The account's mark, one of the eight account colours of web/DESIGN.md,
    -- given at connect: the lowest the member's other accounts use least.
    color                    smallint NOT NULL CHECK (color BETWEEN 1 AND 8),
    -- As users_email_lowercase: emailaddress.Parse lowercases A to Z.
    CONSTRAINT connected_accounts_email_lowercase CHECK (email = lower(email COLLATE "C"))
);

-- One member connects a provider's account once; two members may each connect
-- it. Two providers may issue the same sub. It also serves listing a member's
-- accounts.
CREATE UNIQUE INDEX connected_accounts_user_provider_subject_idx ON connected_accounts (user_id, provider, oauth_subject);

-- +goose Down
DROP TABLE connected_accounts;
