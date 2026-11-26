-- +goose Up
-- Addresses are saved as emailaddress.Parse returns them: ASCII letters
-- lowercased, other letters as typed. Under COLLATE "C", lower() changes only
-- A to Z, whatever the database's locale. users_email_lower_idx already kept
-- users' lower() forms apart, so they cannot collide here, except under a
-- locale where lower('I') is not 'i', e.g. Turkish: there two users such as
-- I@x and i@x could exist, and this migration stops with a unique violation,
-- changing nothing.
UPDATE users SET email = lower(email COLLATE "C");
UPDATE email_verification_links SET address = lower(address COLLATE "C");
UPDATE sign_in_attempts SET email = lower(email COLLATE "C");
UPDATE system_email SET from_address = lower(from_address COLLATE "C");

DROP INDEX users_email_lower_idx;
ALTER TABLE users
    ADD CONSTRAINT users_email_key UNIQUE (email),
    ADD CONSTRAINT users_email_lowercase CHECK (email = lower(email COLLATE "C"));

-- +goose Down
-- The addresses' original case is lost. Restoring the index fails if two
-- users' addresses differ only in the case of non-ASCII letters that the
-- database's locale lowercases.
ALTER TABLE users
    DROP CONSTRAINT users_email_lowercase,
    DROP CONSTRAINT users_email_key;
CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email));
