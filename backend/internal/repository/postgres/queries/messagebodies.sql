-- name: ListMessagesNeedingBodies :many
-- The account's messages whose bodies the background fetch takes next,
-- newest first: without parts, received at or after @since, in no mailbox
-- with one of @excluded_roles, and with no failed fetch at or after
-- @failed_since. amplop's Go code chooses all three.
-- ListAccountsWithDueBodies repeats these conditions.
SELECT m.id, m.provider_message_id
FROM messages m
WHERE m.connected_account_id = @connected_account_id
    AND m.internal_date >= @since
    AND (m.body_failed_at IS NULL OR m.body_failed_at < @failed_since::timestamptz)
    AND NOT EXISTS (SELECT 1 FROM message_parts p WHERE p.message_id = m.id)
    AND NOT EXISTS (
        SELECT 1
        FROM message_mailboxes mm
        JOIN mailboxes b ON b.connected_account_id = mm.connected_account_id AND b.id = mm.mailbox_id
        WHERE mm.message_id = m.id AND b.role = ANY (@excluded_roles::text[]))
ORDER BY m.internal_date DESC, m.id
LIMIT @max_messages;

-- name: MarkMessageBodyFailed :execrows
UPDATE messages SET body_failed_at = @failed_at
WHERE connected_account_id = @connected_account_id AND id = @id;

-- name: ListAccountsWithDueBodies :many
-- The accounts whose member is active, which need no reconnect, whose full
-- sync is complete, and which have a message ListMessagesNeedingBodies
-- would list: its conditions are repeated here, so change both together.
SELECT s.connected_account_id
FROM sync_states s
JOIN connected_accounts ca ON ca.id = s.connected_account_id
JOIN users u ON u.id = ca.user_id
WHERE s.status = 'complete'
    AND ca.needs_reconnect_since IS NULL
    AND u.deactivated_at IS NULL
    AND EXISTS (
        SELECT 1
        FROM messages m
        WHERE m.connected_account_id = s.connected_account_id
            AND m.internal_date >= @since
            AND (m.body_failed_at IS NULL OR m.body_failed_at < @failed_since::timestamptz)
            AND NOT EXISTS (SELECT 1 FROM message_parts p WHERE p.message_id = m.id)
            AND NOT EXISTS (
                SELECT 1
                FROM message_mailboxes mm
                JOIN mailboxes b ON b.connected_account_id = mm.connected_account_id AND b.id = mm.mailbox_id
                WHERE mm.message_id = m.id AND b.role = ANY (@excluded_roles::text[])))
ORDER BY s.connected_account_id;
