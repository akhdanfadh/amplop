-- name: InsertSyncState :exec
-- A new account's state: waiting for the full sync saved with it.
INSERT INTO sync_states (connected_account_id, status) VALUES (@connected_account_id, 'waiting');

-- name: LockSyncState :one
-- The position travels as text, empty for none.
SELECT connected_account_id, status, coalesce(position, '')::text AS position, sync_generation,
    failure, failed_at, last_synced_at
FROM sync_states
WHERE connected_account_id = @connected_account_id
-- No key column changes, so foreign key checks on the row need not wait.
FOR NO KEY UPDATE;

-- name: UpdateSyncState :exec
UPDATE sync_states SET
    status = @status,
    position = nullif(@position::text, ''),
    sync_generation = @sync_generation,
    failure = @failure,
    failed_at = @failed_at,
    last_synced_at = @last_synced_at
WHERE connected_account_id = @connected_account_id;

-- name: ReadSyncState :one
-- The same columns as LockSyncState, without the lock.
SELECT connected_account_id, status, coalesce(position, '')::text AS position, sync_generation,
    failure, failed_at, last_synced_at
FROM sync_states
WHERE connected_account_id = @connected_account_id;

-- name: ListDueSyncs :many
-- The accounts whose provider work may run, i.e. whose member is active and
-- which need no reconnect, and that are complete, waiting, or failed at or
-- before @failed_before.
SELECT s.connected_account_id, s.status, coalesce(s.position, '')::text AS position, s.failure
FROM sync_states s
JOIN connected_accounts ca ON ca.id = s.connected_account_id
JOIN users u ON u.id = ca.user_id
WHERE ca.needs_reconnect_since IS NULL
    AND u.deactivated_at IS NULL
    AND (s.status IN ('complete', 'waiting')
        OR (s.status = 'failed' AND s.failed_at <= @failed_before))
ORDER BY s.connected_account_id;

-- name: ConfirmSynced :exec
-- Records each account's check time as when its copy last matched the
-- provider, where it is still complete at the position checked with no
-- failure, and only forward, so a sync that finished since keeps its own.
UPDATE sync_states s
SET last_synced_at = c.checked_at
FROM (
    SELECT unnest(@connected_account_ids::bigint[]) AS connected_account_id,
        unnest(@positions::text[]) AS position,
        unnest(@checked_ats::timestamptz[]) AS checked_at
) c
WHERE s.connected_account_id = c.connected_account_id
    AND s.position = c.position
    AND s.status = 'complete'
    AND s.failure IS NULL
    -- Checked here, not with greatest(), so a time that would not move
    -- writes no new row version.
    AND (s.last_synced_at IS NULL OR s.last_synced_at < c.checked_at);
