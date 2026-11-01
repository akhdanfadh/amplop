-- +goose Up
-- An event names who did it and which account it was done to; either can be
-- absent, but not both. Existing admin-recovered rows keep their target.
ALTER TABLE audit_events RENAME COLUMN user_id TO target_user_id; -- NULL: no account, e.g. a settings save
ALTER TABLE audit_events ALTER COLUMN target_user_id DROP NOT NULL;
ALTER TABLE audit_events ADD COLUMN actor_user_id bigint REFERENCES users (id); -- NULL: the host operator, or someone without a session, e.g. an email verification link holder
ALTER TABLE audit_events ADD CONSTRAINT audit_events_party
    CHECK (target_user_id IS NOT NULL OR actor_user_id IS NOT NULL);

-- +goose Down
-- Loses history: the old table cannot hold events without a target, so they
-- are removed, and the actor of every remaining event is dropped.
DELETE FROM audit_events WHERE target_user_id IS NULL;
ALTER TABLE audit_events DROP CONSTRAINT audit_events_party;
ALTER TABLE audit_events DROP COLUMN actor_user_id;
ALTER TABLE audit_events ALTER COLUMN target_user_id SET NOT NULL;
ALTER TABLE audit_events RENAME COLUMN target_user_id TO user_id;
