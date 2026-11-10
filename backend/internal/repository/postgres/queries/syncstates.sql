-- name: LockSyncState :one
-- The position travels as text, empty for none.
SELECT connected_account_id, status, coalesce(position, '')::text AS position, sync_generation,
    pages_synced, messages_synced, failure, last_synced_at
FROM sync_states
WHERE connected_account_id = @connected_account_id
-- No key column changes, so foreign key checks on the row need not wait.
FOR NO KEY UPDATE;

-- name: UpdateSyncState :exec
UPDATE sync_states SET
    status = @status,
    position = nullif(@position::text, ''),
    sync_generation = @sync_generation,
    pages_synced = @pages_synced,
    messages_synced = @messages_synced,
    failure = @failure,
    last_synced_at = @last_synced_at
WHERE connected_account_id = @connected_account_id;
