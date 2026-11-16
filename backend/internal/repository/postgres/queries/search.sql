-- name: ReadAccountOwner :one
-- The member whose connected account it is.
SELECT user_id FROM connected_accounts WHERE id = @id;

-- name: ListIndexSources :many
-- Owner's messages among the IDs, with what their search documents need:
-- the IDs of the mailboxes each is in, whether its parts are stored, and
-- its conversation's mailboxes and star, none for a message without one.
SELECT m.id, m.connected_account_id, m.provider_thread_id, m.internal_date,
    m.subject, m.from_addresses, m.to_addresses, m.cc_addresses,
    coalesce((
        SELECT array_agg(mm.mailbox_id ORDER BY mm.mailbox_id)
        FROM message_mailboxes mm
        WHERE mm.message_id = m.id
    ), '{}')::uuid[] AS mailbox_ids,
    EXISTS (SELECT 1 FROM message_parts p WHERE p.message_id = m.id)::boolean AS has_parts,
    coalesce((
        SELECT array_agg(cm.mailbox_id ORDER BY cm.mailbox_id)
        FROM conversation_mailboxes cm
        WHERE cm.conversation_id = c.id
    ), '{}')::uuid[] AS conversation_mailbox_ids,
    coalesce(c.flagged, false)::boolean AS conversation_flagged
FROM messages m
JOIN connected_accounts a ON a.id = m.connected_account_id
LEFT JOIN conversations c ON c.connected_account_id = m.connected_account_id
    AND c.provider_thread_id = m.provider_thread_id
WHERE a.user_id = @owner
    AND m.id = ANY (@ids::uuid[]);

-- name: ListUserIDs :many
-- Every member, with mail or not.
SELECT id FROM users ORDER BY id;

-- name: UserExists :one
SELECT EXISTS (SELECT 1 FROM users WHERE id = @id)::boolean;

-- name: ListUserMessageIDs :many
-- Every message of the member's connected accounts.
SELECT m.id
FROM messages m
JOIN connected_accounts a ON a.id = m.connected_account_id
WHERE a.user_id = @owner
ORDER BY m.id;

-- name: ListMailboxIDsWithRoles :many
-- The member's mailboxes with any of the roles, in every account.
SELECT b.id
FROM mailboxes b
JOIN connected_accounts a ON a.id = b.connected_account_id
WHERE a.user_id = @owner
    AND b.role = ANY (@roles::text[])
ORDER BY b.id;

-- name: ListFoundMessages :many
-- The member's messages among the IDs that are in no mailbox with any of
-- the roles, as a search result shows them.
SELECT m.id, m.connected_account_id, m.internal_date, m.from_addresses,
    m.subject, m.preview, m.seen, m.flagged
FROM messages m
JOIN connected_accounts a ON a.id = m.connected_account_id
WHERE a.user_id = @owner
    AND m.id = ANY (@ids::uuid[])
    AND NOT EXISTS (
        SELECT 1
        FROM message_mailboxes mm
        JOIN mailboxes b ON b.connected_account_id = mm.connected_account_id AND b.id = mm.mailbox_id
        WHERE mm.message_id = m.id
            AND b.role = ANY (@roles::text[])
    );
