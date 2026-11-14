-- name: UpsertMailbox :batchexec
-- A mailbox the account already has keeps its row and ID, and so its
-- messages.
INSERT INTO mailboxes (connected_account_id, provider_mailbox_id, name, role, made_by, provider_parent_id)
VALUES (@connected_account_id, @provider_mailbox_id, @name, @role, @made_by, @provider_parent_id)
ON CONFLICT (connected_account_id, provider_mailbox_id) DO UPDATE SET
    name = excluded.name,
    role = excluded.role,
    made_by = excluded.made_by,
    provider_parent_id = excluded.provider_parent_id;

-- name: DeleteUnlistedMailboxes :exec
-- The account's mailboxes the provider no longer lists, with their
-- memberships.
DELETE FROM mailboxes
WHERE connected_account_id = @connected_account_id
    AND provider_mailbox_id <> ALL (@provider_mailbox_ids::text[]);

-- name: ListMailboxProviderIDs :many
SELECT provider_mailbox_id FROM mailboxes WHERE connected_account_id = @connected_account_id;
