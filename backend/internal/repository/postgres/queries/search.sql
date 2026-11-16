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

-- name: ListRoleMailboxes :many
-- The member's mailboxes that have a role, in every account.
SELECT b.id, b.role::text AS role
FROM mailboxes b
JOIN connected_accounts a ON a.id = b.connected_account_id
WHERE a.user_id = @owner
    AND b.role IS NOT NULL
ORDER BY b.id;

-- name: ListMemberLabelIDs :many
-- Those of the IDs that are the member's labels: mailboxes of theirs with
-- no role that they made.
SELECT b.id
FROM mailboxes b
JOIN connected_accounts a ON a.id = b.connected_account_id
WHERE a.user_id = @owner
    AND b.id = ANY (@ids::uuid[])
    AND b.role IS NULL
    AND b.made_by = 'member'
ORDER BY b.id;

-- name: ListFoundMessages :many
-- The member's messages among the IDs that have a conversation, as a
-- search result shows them, with the roles of their mailboxes, an empty
-- role for none, and their conversation's ID.
SELECT m.id, m.connected_account_id, m.internal_date, m.from_addresses,
    m.subject, m.preview, c.id AS conversation_id,
    coalesce((
        SELECT array_agg(coalesce(b.role, '') ORDER BY b.id)
        FROM message_mailboxes mm
        JOIN mailboxes b ON b.connected_account_id = mm.connected_account_id AND b.id = mm.mailbox_id
        WHERE mm.message_id = m.id
    ), '{}')::text[] AS mailbox_roles
FROM messages m
JOIN connected_accounts a ON a.id = m.connected_account_id
JOIN conversations c ON c.connected_account_id = m.connected_account_id
    AND c.provider_thread_id = m.provider_thread_id
WHERE a.user_id = @owner
    AND m.id = ANY (@ids::uuid[]);
