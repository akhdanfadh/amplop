-- +goose Up
-- Every saved reset link counts toward its account's limits. The request's
-- attempt counts its first link; a later link for the same attempt, or one
-- whose attempt is gone, gets a row of its own, with the account and the
-- link's time and no client: client_prefix is NULL, so it counts for the
-- account but not toward the client's limit, which counts requests.
ALTER TABLE password_reset_attempts ALTER COLUMN client_prefix DROP NOT NULL;

-- +goose Down
DELETE FROM password_reset_attempts WHERE client_prefix IS NULL;
ALTER TABLE password_reset_attempts ALTER COLUMN client_prefix SET NOT NULL;
