-- name: UpsertMailbox :batchexec
-- A mailbox the account already has keeps its row and ID, and so its
-- messages.
INSERT INTO mailboxes (connected_account_id, provider_mailbox_id, name, role)
VALUES (@connected_account_id, @provider_mailbox_id, @name, @role)
ON CONFLICT (connected_account_id, provider_mailbox_id) DO UPDATE SET
    name = excluded.name,
    role = excluded.role;

-- name: DeleteUnlistedMailboxes :exec
-- The account's mailboxes the provider no longer lists, with their
-- memberships.
DELETE FROM mailboxes
WHERE connected_account_id = @connected_account_id
    AND provider_mailbox_id <> ALL (@provider_mailbox_ids::text[]);
