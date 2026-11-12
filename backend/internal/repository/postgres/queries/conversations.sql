-- Conversations are derived from the synchronized mail, thread by thread, in
-- the transaction that writes the mail (ADR001, "Conversations"). The rules
-- are conversation.Derive's; these queries load what it reads and save what
-- it returns.

-- name: ListThreadMessages :many
-- The messages of the account's threads named, each with the IDs and roles
-- of its mailboxes, in ID order, an empty role for none.
SELECT m.id, m.provider_thread_id, m.internal_date, m.subject, m.preview, m.from_addresses,
    m.seen, m.flagged,
    coalesce(array_agg(mb.id ORDER BY mb.id) FILTER (WHERE mb.id IS NOT NULL), '{}')::uuid[] AS mailbox_ids,
    coalesce(array_agg(coalesce(mb.role, '') ORDER BY mb.id) FILTER (WHERE mb.id IS NOT NULL), '{}')::text[] AS mailbox_roles
FROM messages m
LEFT JOIN message_mailboxes mm ON mm.message_id = m.id
LEFT JOIN mailboxes mb ON mb.id = mm.mailbox_id
WHERE m.connected_account_id = @connected_account_id
    AND m.provider_thread_id = ANY (@provider_thread_ids::text[])
GROUP BY m.id;

-- name: DeleteThreadConversations :exec
-- The account's conversations of the threads named, with their mailboxes.
DELETE FROM conversations
WHERE connected_account_id = @connected_account_id
    AND provider_thread_id = ANY (@provider_thread_ids::text[]);

-- name: UpsertConversation :batchone
-- A thread that has a conversation already keeps its row and ID.
INSERT INTO conversations (connected_account_id, provider_thread_id, latest_date, subject, preview,
    latest_from, participants, message_count, unread, flagged, in_all_mail)
VALUES (@connected_account_id, @provider_thread_id, @latest_date, @subject, @preview,
    @latest_from, @participants, @message_count, @unread, @flagged, @in_all_mail)
ON CONFLICT (connected_account_id, provider_thread_id) DO UPDATE SET
    latest_date = excluded.latest_date,
    subject = excluded.subject,
    preview = excluded.preview,
    latest_from = excluded.latest_from,
    participants = excluded.participants,
    message_count = excluded.message_count,
    unread = excluded.unread,
    flagged = excluded.flagged,
    in_all_mail = excluded.in_all_mail
RETURNING id;

-- name: DeleteConversationMailboxes :exec
DELETE FROM conversation_mailboxes WHERE conversation_id = ANY (@conversation_ids::uuid[]);

-- name: InsertConversationMailboxes :exec
-- One place per position of the two lists, which have the same length,
-- each with copies of its conversation's date and unread.
INSERT INTO conversation_mailboxes (connected_account_id, conversation_id, mailbox_id, latest_date, unread)
SELECT c.connected_account_id, c.id, pair.mailbox_id, c.latest_date, c.unread
FROM (
    SELECT unnest(@conversation_ids::uuid[]) AS conversation_id,
        unnest(@mailbox_ids::uuid[]) AS mailbox_id
) AS pair
JOIN conversations c ON c.id = pair.conversation_id
WHERE c.connected_account_id = @connected_account_id;
