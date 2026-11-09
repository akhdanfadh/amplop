-- name: LockSyncState :one
-- The history ID travels as decimal text, empty for none: pgx has no Go
-- integer for numeric, and Gmail's need all 64 unsigned bits.
SELECT connected_account_id, status, coalesce(history_id::text, '')::text AS history_id, sync_generation,
    pages_synced, messages_synced, failure, last_synced_at
FROM sync_states
WHERE connected_account_id = @connected_account_id
-- No key column changes, so foreign key checks on the row need not wait.
FOR NO KEY UPDATE;

-- name: UpdateSyncState :exec
UPDATE sync_states SET
    status = @status,
    history_id = sqlc.narg(history_id)::text::numeric,
    sync_generation = @sync_generation,
    pages_synced = @pages_synced,
    messages_synced = @messages_synced,
    failure = @failure,
    last_synced_at = @last_synced_at
WHERE connected_account_id = @connected_account_id;
