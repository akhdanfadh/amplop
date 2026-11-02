-- +goose Up
-- Whether an administrator has opened public registration. The always-true key
-- allows one row. It is created closed, so a new installation starts closed
-- and every writer has a row to lock.
CREATE TABLE registration (
    id   boolean PRIMARY KEY DEFAULT true CHECK (id),
    open boolean NOT NULL DEFAULT false
);
INSERT INTO registration DEFAULT VALUES;

-- +goose Down
DROP TABLE registration;
