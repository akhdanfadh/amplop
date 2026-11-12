-- name: UpsertMessage :batchone
-- A message the account already has keeps its row and ID, so a repeated
-- sync step adds nothing.
INSERT INTO messages (connected_account_id, provider_message_id, provider_thread_id,
    internal_date, subject, from_addresses, reply_to_addresses, to_addresses,
    cc_addresses, preview, seen, flagged, internet_message_id, in_reply_to,
    internet_references, sync_generation, provider_version)
VALUES (@connected_account_id, @provider_message_id, @provider_thread_id,
    @internal_date, @subject, @from_addresses, @reply_to_addresses, @to_addresses,
    @cc_addresses, @preview, @seen, @flagged, @internet_message_id, @in_reply_to,
    @internet_references, @sync_generation, @provider_version)
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
    sync_generation = excluded.sync_generation,
    provider_version = excluded.provider_version
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

-- name: DeleteMessages :one
-- Returns each deleted message's thread, whose conversation changes, and
-- the blob IDs their parts named. Every part of the statement reads the
-- rows as they were before it, so the select sees the parts the delete
-- cascades to.
WITH deleted AS (
    DELETE FROM messages m
    WHERE m.connected_account_id = @connected_account_id
        AND m.provider_message_id = ANY (@provider_message_ids::text[])
    RETURNING m.id, m.provider_thread_id
)
SELECT coalesce((
        SELECT array_agg(d.provider_thread_id) FROM deleted d
    ), '{}')::text[] AS thread_ids,
    coalesce((
        SELECT array_agg(p.blob_id)
        FROM message_parts p
        JOIN deleted d ON d.id = p.message_id
        WHERE p.blob_id IS NOT NULL
    ), '{}')::uuid[] AS blob_ids;

-- name: DeleteEarlierMessages :one
-- Deletes up to @max_deleted of the messages no step of the account's
-- current generation saved. Returns how many, and their threads and the
-- blob IDs their parts named, as DeleteMessages does.
WITH deleted AS (
    DELETE FROM messages m
    WHERE m.id IN (
        SELECT earlier.id FROM messages earlier
        WHERE earlier.connected_account_id = @connected_account_id
            AND earlier.sync_generation < @sync_generation
        LIMIT @max_deleted
    )
    RETURNING m.id, m.provider_thread_id
)
SELECT (SELECT count(*) FROM deleted)::bigint AS deleted,
    coalesce((
        SELECT array_agg(d.provider_thread_id) FROM deleted d
    ), '{}')::text[] AS thread_ids,
    coalesce((
        SELECT array_agg(p.blob_id)
        FROM message_parts p
        JOIN deleted d ON d.id = p.message_id
        WHERE p.blob_id IS NOT NULL
    ), '{}')::uuid[] AS blob_ids;

-- name: UpdateMessageFlags :many
-- Sets the flags and version of the account's messages named, one per
-- position of the four lists, which have the same length. A message the
-- account does not have is skipped, and so is one whose stored version is
-- newer. Returns the IDs and threads of those it set.
UPDATE messages m SET
    seen = u.seen,
    flagged = u.flagged,
    provider_version = u.provider_version
FROM (
    SELECT unnest(@provider_message_ids::text[]) AS provider_message_id,
        unnest(@seen::boolean[]) AS seen,
        unnest(@flagged::boolean[]) AS flagged,
        unnest(@provider_versions::bigint[]) AS provider_version
) AS u
WHERE m.connected_account_id = @connected_account_id
    AND m.provider_message_id = u.provider_message_id
    AND m.provider_version <= u.provider_version
RETURNING m.id, m.provider_message_id, m.provider_thread_id;

-- name: OldestMessage :one
-- When the provider received the account's oldest message, and whether it
-- has one; the time is the epoch when it has none.
SELECT (count(*) > 0)::boolean AS found,
    coalesce(min(internal_date), 'epoch')::timestamptz AS oldest
FROM messages
WHERE connected_account_id = @connected_account_id;
