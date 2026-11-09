-- name: UpsertMessage :batchone
-- A message the account already has keeps its row and ID, so a repeated
-- sync step adds nothing.
INSERT INTO messages (connected_account_id, provider_message_id, provider_thread_id,
    internal_date, subject, from_addresses, reply_to_addresses, to_addresses,
    cc_addresses, preview, seen, flagged, internet_message_id, in_reply_to,
    internet_references, sync_generation)
VALUES (@connected_account_id, @provider_message_id, @provider_thread_id,
    @internal_date, @subject, @from_addresses, @reply_to_addresses, @to_addresses,
    @cc_addresses, @preview, @seen, @flagged, @internet_message_id, @in_reply_to,
    @internet_references, @sync_generation)
ON CONFLICT (connected_account_id, provider_message_id) DO UPDATE SET
    provider_thread_id = excluded.provider_thread_id,
    internal_date = excluded.internal_date,
    subject = excluded.subject,
    from_addresses = excluded.from_addresses,
    reply_to_addresses = excluded.reply_to_addresses,
    to_addresses = excluded.to_addresses,
    cc_addresses = excluded.cc_addresses,
    preview = excluded.preview,
    seen = excluded.seen,
    flagged = excluded.flagged,
    internet_message_id = excluded.internet_message_id,
    in_reply_to = excluded.in_reply_to,
    internet_references = excluded.internet_references,
    sync_generation = excluded.sync_generation
RETURNING id;

-- name: DeleteMessageMailboxes :exec
DELETE FROM message_mailboxes WHERE message_id = ANY (@message_ids::uuid[]);

-- name: InsertMessageMailboxes :execrows
-- One membership per position of the two lists, which have the same
-- length; a pair whose mailbox the account does not have inserts nothing.
INSERT INTO message_mailboxes (connected_account_id, message_id, mailbox_id)
SELECT @connected_account_id, pair.message_id, mb.id
FROM (
    SELECT unnest(@message_ids::uuid[]) AS message_id,
        unnest(@provider_mailbox_ids::text[]) AS provider_mailbox_id
) AS pair
JOIN mailboxes mb ON mb.connected_account_id = @connected_account_id
    AND mb.provider_mailbox_id = pair.provider_mailbox_id;

-- name: DeleteMessages :exec
DELETE FROM messages
WHERE connected_account_id = @connected_account_id
    AND provider_message_id = ANY (@provider_message_ids::text[]);

-- name: DeleteEarlierMessages :exec
-- The messages no step of the account's current generation saved.
DELETE FROM messages
WHERE connected_account_id = @connected_account_id AND sync_generation < @sync_generation;
