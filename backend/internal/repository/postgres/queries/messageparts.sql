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
