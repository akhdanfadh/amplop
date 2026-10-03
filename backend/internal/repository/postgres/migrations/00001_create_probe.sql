-- The probe exercises the whole stack, from browser form to database
-- transaction. Remove it before the first release.
-- The CHECKs repeat the API's limits, so a bug that skips the API checks
-- still cannot store a bad row.

-- +goose Up
CREATE TABLE probe_counter (
    id    integer PRIMARY KEY CHECK (id = 1), -- one row only
    total bigint NOT NULL CHECK (total >= 0)
);

INSERT INTO probe_counter (id, total) VALUES (1, 0);

CREATE TABLE probe_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    note        text NOT NULL CHECK (char_length(note) <= 120 AND note ~ '\S'),
    amount      integer NOT NULL CHECK (amount BETWEEN 1 AND 10),
    highlighted boolean NOT NULL,
    tag         text CHECK (char_length(tag) BETWEEN 1 AND 40), -- NULL: no tag
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE probe_events;
DROP TABLE probe_counter;
