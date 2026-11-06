-- +goose Up
-- inbok names an email `email` everywhere else; "address" also means a
-- client's IP address.
ALTER TABLE email_verification_links RENAME COLUMN address TO email;

-- +goose Down
ALTER TABLE email_verification_links RENAME COLUMN email TO address;
