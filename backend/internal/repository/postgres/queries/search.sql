-- name: ReadAccountOwner :one
-- The member whose connected account it is.
SELECT user_id FROM connected_accounts WHERE id = @id;

-- name: ListIndexSources :many
-- Owner's messages among the IDs, with what their search documents need:
-- the IDs of the mailboxes each is in, and whether its parts are stored.
SELECT m.id, m.connected_account_id, m.provider_thread_id, m.internal_date,
    m.subject, m.from_addresses, m.to_addresses, m.cc_addresses,
    coalesce((
        SELECT array_agg(mm.mailbox_id ORDER BY mm.mailbox_id)
        FROM message_mailboxes mm
        WHERE mm.message_id = m.id
    ), '{}')::uuid[] AS mailbox_ids,
    EXISTS (SELECT 1 FROM message_parts p WHERE p.message_id = m.id)::boolean AS has_parts
FROM messages m
JOIN connected_accounts a ON a.id = m.connected_account_id
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
