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
    latest_from, participants, message_count, unread, flagged, in_all_mail, newest_message_id)
VALUES (@connected_account_id, @provider_thread_id, @latest_date, @subject, @preview,
    @latest_from, @participants, @message_count, @unread, @flagged, @in_all_mail, @newest_message_id)
ON CONFLICT (connected_account_id, provider_thread_id) DO UPDATE SET
    latest_date = excluded.latest_date,
    subject = excluded.subject,
    preview = excluded.preview,
    latest_from = excluded.latest_from,
    participants = excluded.participants,
    message_count = excluded.message_count,
    unread = excluded.unread,
    flagged = excluded.flagged,
    in_all_mail = excluded.in_all_mail,
    newest_message_id = excluded.newest_message_id
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

-- name: ListMemberConnectedAccountIDs :many
-- The member's accounts, or the one named, oldest first.
SELECT id FROM connected_accounts
WHERE user_id = @user_id AND (@account_id::bigint = 0 OR id = @account_id)
ORDER BY created_at, id;

-- name: ListRoleMailboxIDs :many
SELECT id FROM mailboxes
WHERE connected_account_id = ANY (@account_ids::bigint[]) AND role = @role::text;

-- name: ReadMemberLabelAccount :one
-- The account of the member's label, which the member names as a place:
-- a mailbox of theirs with no role that they made. Other mailboxes are
-- places only as views, by role.
SELECT mb.connected_account_id
FROM mailboxes mb
JOIN connected_accounts ca ON ca.id = mb.connected_account_id
WHERE mb.id = @id AND ca.user_id = @user_id AND mb.role IS NULL AND mb.made_by = 'member';

-- name: ListMailboxConversationIDs :many
-- A page of the mailboxes' conversations, newest first, older than the
-- cursor: each mailbox's page from its index, merged.
SELECT DISTINCT page.conversation_id, page.latest_date
FROM unnest(@mailbox_ids::uuid[]) AS box (id)
CROSS JOIN LATERAL (
    SELECT cm.conversation_id, cm.latest_date FROM conversation_mailboxes cm
    WHERE cm.mailbox_id = box.id
        AND (cm.latest_date, cm.conversation_id) < (@before_date::timestamptz, @before_id::uuid)
    ORDER BY cm.latest_date DESC, cm.conversation_id DESC
    LIMIT @max_rows
) AS page
ORDER BY page.latest_date DESC, page.conversation_id DESC
LIMIT @max_rows;

-- name: ListAllMailConversationIDs :many
-- As ListMailboxConversationIDs, for the accounts' visible conversations.
SELECT page.id, page.latest_date
FROM unnest(@account_ids::bigint[]) AS account (id)
CROSS JOIN LATERAL (
    SELECT c.id, c.latest_date FROM conversations c
    WHERE c.connected_account_id = account.id AND c.in_all_mail
        AND (c.latest_date, c.id) < (@before_date::timestamptz, @before_id::uuid)
    ORDER BY c.latest_date DESC, c.id DESC
    LIMIT @max_rows
) AS page
ORDER BY page.latest_date DESC, page.id DESC
LIMIT @max_rows;

-- name: ListStarredConversationIDs :many
-- As ListAllMailConversationIDs, for the flagged ones.
SELECT page.id, page.latest_date
FROM unnest(@account_ids::bigint[]) AS account (id)
CROSS JOIN LATERAL (
    SELECT c.id, c.latest_date FROM conversations c
    WHERE c.connected_account_id = account.id AND c.in_all_mail AND c.flagged
        AND (c.latest_date, c.id) < (@before_date::timestamptz, @before_id::uuid)
    ORDER BY c.latest_date DESC, c.id DESC
    LIMIT @max_rows
) AS page
ORDER BY page.latest_date DESC, page.id DESC
LIMIT @max_rows;

-- name: ReadConversationRows :many
-- The member's conversations of those named, each with the places it is
-- in: the views by role, and the member's own labels by name.
SELECT c.id, c.connected_account_id, c.latest_date, c.subject, c.preview, c.latest_from,
    c.participants, c.message_count, c.unread, c.flagged, c.in_all_mail,
    coalesce(places.inbox, false)::boolean AS in_inbox,
    coalesce(places.sent, false)::boolean AS in_sent,
    coalesce(places.spam, false)::boolean AS in_spam,
    coalesce(places.trash, false)::boolean AS in_trash,
    coalesce(places.label_ids, '{}')::uuid[] AS label_ids
FROM conversations c
JOIN connected_accounts ca ON ca.id = c.connected_account_id
LEFT JOIN LATERAL (
    SELECT bool_or(mb.role = 'inbox') AS inbox,
        bool_or(mb.role = 'sent') AS sent,
        bool_or(mb.role = 'junk') AS spam,
        bool_or(mb.role = 'trash') AS trash,
        array_agg(mb.id ORDER BY mb.name, mb.id) FILTER (WHERE mb.role IS NULL AND mb.made_by = 'member') AS label_ids
    FROM conversation_mailboxes cm
    JOIN mailboxes mb ON mb.id = cm.mailbox_id
    WHERE cm.conversation_id = c.id
) AS places ON true
WHERE c.id = ANY (@ids::uuid[]) AND ca.user_id = @user_id;

-- name: ReadConversationSource :one
-- Where the member's conversation comes from: its account's provider and
-- address, and its thread there.
SELECT ca.provider, ca.email, c.provider_thread_id
FROM conversations c
JOIN connected_accounts ca ON ca.id = c.connected_account_id
WHERE c.id = @id AND ca.user_id = @user_id;

-- name: ListConversationMessages :many
-- Every message of the conversation's thread, drafts too, oldest first,
-- each with the IDs, roles, and makers of its mailboxes by name, an empty
-- role for none.
SELECT m.id, m.internal_date, m.subject, m.from_addresses, m.reply_to_addresses, m.to_addresses,
    m.cc_addresses, m.preview, m.seen, m.flagged, m.internet_message_id, m.in_reply_to,
    coalesce(array_agg(mb.id ORDER BY mb.name, mb.id) FILTER (WHERE mb.id IS NOT NULL), '{}')::uuid[] AS mailbox_ids,
    coalesce(array_agg(coalesce(mb.role, '') ORDER BY mb.name, mb.id) FILTER (WHERE mb.id IS NOT NULL), '{}')::text[] AS mailbox_roles,
    coalesce(array_agg(mb.made_by ORDER BY mb.name, mb.id) FILTER (WHERE mb.id IS NOT NULL), '{}')::text[] AS mailbox_makers
FROM conversations c
JOIN messages m ON m.connected_account_id = c.connected_account_id
    AND m.provider_thread_id = c.provider_thread_id
LEFT JOIN message_mailboxes mm ON mm.message_id = m.id
LEFT JOIN mailboxes mb ON mb.id = mm.mailbox_id
WHERE c.id = @id
GROUP BY m.id
ORDER BY m.internal_date, m.id;

-- name: ListConversationMessageParts :many
-- The stored parts of the conversation's messages whose content was
-- fetched, by message, each message's depth first as ListMessageParts.
SELECT p.message_id, p.path, p.provider_part_id, p.media_type, p.charset, p.filename, p.size,
    p.disposition, p.mime_content_id, p.related_start, p.kind, p.blob_id
FROM conversations c
JOIN messages m ON m.connected_account_id = c.connected_account_id
    AND m.provider_thread_id = c.provider_thread_id
JOIN message_parts p ON p.connected_account_id = m.connected_account_id
    AND p.message_id = m.id
WHERE c.id = @id
ORDER BY p.message_id, p.path;

-- name: CountMailboxConversationsAfter :one
SELECT count(DISTINCT conversation_id)::integer FROM conversation_mailboxes
WHERE mailbox_id = ANY (@mailbox_ids::uuid[]) AND latest_date > @after;

-- name: CountAllMailConversationsAfter :one
SELECT count(*)::integer FROM conversations
WHERE connected_account_id = ANY (@account_ids::bigint[]) AND in_all_mail AND latest_date > @after;

-- name: CountStarredConversationsAfter :one
SELECT count(*)::integer FROM conversations
WHERE connected_account_id = ANY (@account_ids::bigint[]) AND in_all_mail AND flagged
    AND latest_date > @after;

-- name: ListMailboxRoleUnreadCounts :many
-- For each role of the member's accounts' mailboxes, how many
-- conversations in a mailbox of that role are unread, each once.
SELECT mb.connected_account_id, mb.role::text AS role,
    count(DISTINCT cm.conversation_id)::integer AS unread
FROM mailboxes mb
JOIN connected_accounts ca ON ca.id = mb.connected_account_id
LEFT JOIN conversation_mailboxes cm ON cm.mailbox_id = mb.id AND cm.unread
WHERE ca.user_id = @user_id AND mb.role IS NOT NULL
GROUP BY mb.connected_account_id, mb.role;

-- name: ListMailboxLabelUnreadCounts :many
-- The member's own labels, those with no role that the member made, by
-- name, with how many of their conversations are unread, and the label
-- each nests under: one of the same account's own labels, else none.
SELECT mb.connected_account_id, mb.id, mb.name, parent.id AS parent_id,
    (SELECT count(*) FROM conversation_mailboxes cm WHERE cm.mailbox_id = mb.id AND cm.unread)::integer AS unread
FROM mailboxes mb
JOIN connected_accounts ca ON ca.id = mb.connected_account_id
LEFT JOIN mailboxes parent ON parent.connected_account_id = mb.connected_account_id
    AND parent.provider_mailbox_id = mb.provider_parent_id
    AND parent.role IS NULL AND parent.made_by = 'member'
WHERE ca.user_id = @user_id AND mb.role IS NULL AND mb.made_by = 'member'
ORDER BY mb.name, mb.id;

-- name: ListConversationUnreadCounts :many
-- The member's accounts, oldest first, with how many of their visible
-- conversations, and of the flagged ones, are unread.
SELECT ca.id,
    (SELECT count(*) FROM conversations c
        WHERE c.connected_account_id = ca.id AND c.in_all_mail AND c.unread)::integer AS all_mail,
    (SELECT count(*) FROM conversations c
        WHERE c.connected_account_id = ca.id AND c.in_all_mail AND c.unread AND c.flagged)::integer AS starred
FROM connected_accounts ca
WHERE ca.user_id = @user_id
ORDER BY ca.created_at, ca.id;

-- name: ListConversationThreads :many
-- The member's conversations of those named, with their threads and their
-- newest messages, which an action changes at the provider. The newest
-- message is derived with the row, in the transaction that writes the
-- mail, so it exists.
SELECT c.id, c.connected_account_id, c.provider_thread_id,
    m.provider_message_id AS provider_newest_message_id
FROM conversations c
JOIN connected_accounts ca ON ca.id = c.connected_account_id
JOIN messages m ON m.connected_account_id = c.connected_account_id AND m.id = c.newest_message_id
WHERE c.id = ANY (@ids::uuid[]) AND ca.user_id = @user_id;
