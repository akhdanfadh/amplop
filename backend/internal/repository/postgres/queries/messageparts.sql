-- name: MessageHasParts :one
-- Whether the account's message has parts saved; no row when the account
-- has no such message.
SELECT EXISTS (SELECT 1 FROM message_parts p WHERE p.message_id = m.id)::boolean AS has_parts
FROM messages m
WHERE m.connected_account_id = @connected_account_id AND m.id = @id;

-- name: InsertMessagePart :batchexec
INSERT INTO message_parts (connected_account_id, message_id, path, provider_part_id,
    media_type, charset, filename, size, disposition, mime_content_id, related_start,
    kind, blob_id)
VALUES (@connected_account_id, @message_id, @path, @provider_part_id,
    @media_type, @charset, @filename, @size, @disposition, @mime_content_id, @related_start,
    @kind, @blob_id);

-- name: ListMessageParts :many
-- The parts depth first, each after its parent and its earlier siblings.
SELECT path, provider_part_id, media_type, charset, filename, size, disposition,
    mime_content_id, related_start, kind, blob_id
FROM message_parts
WHERE connected_account_id = @connected_account_id AND message_id = @message_id
ORDER BY path;

-- name: ListUnusedBlobs :many
-- Those of the blob IDs no part names.
SELECT given.id::uuid AS id
FROM unnest(@ids::uuid[]) AS given (id)
WHERE NOT EXISTS (SELECT 1 FROM message_parts p WHERE p.blob_id = given.id);

-- name: GetMessageProviderID :one
SELECT provider_message_id FROM messages
WHERE connected_account_id = @connected_account_id AND id = @id;

-- name: SetMessagePartBlob :one
-- Names @blob_id as the part's content unless the part names one already,
-- and returns the blob the part names, with the part's kind.
UPDATE message_parts SET blob_id = coalesce(blob_id, @blob_id::uuid)
WHERE connected_account_id = @connected_account_id AND message_id = @message_id AND path = @path
RETURNING blob_id, kind;

-- name: GetUserMessageAccount :one
-- The connected account of the user's message; no row when none of the
-- user's accounts has it.
SELECT m.connected_account_id
FROM messages m
JOIN connected_accounts a ON a.id = m.connected_account_id
WHERE m.id = @id AND a.user_id = @user_id;
