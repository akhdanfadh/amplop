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

-- name: ListPlaceMailboxes :many
-- The member's accounts' mailboxes a conversation change can add to or
-- take out of: those of roles, and the member's own labels.
SELECT mb.connected_account_id, mb.id, mb.role, mb.provider_mailbox_id
FROM mailboxes mb
JOIN connected_accounts ca ON ca.id = mb.connected_account_id
WHERE ca.user_id = @user_id
    AND (mb.role = ANY (@roles::text[]) OR (mb.role IS NULL AND mb.made_by = 'member'))
ORDER BY mb.connected_account_id, mb.id;

-- name: ReadMemberAccountExists :one
SELECT EXISTS (
    SELECT 1 FROM connected_accounts WHERE id = @connected_account_id AND user_id = @user_id
);

-- name: ListAccountLabels :many
-- The account's own labels, those with no role that the member made, by
-- name, each with the label it nests under: one of the same account's own
-- labels, else none.
SELECT mb.id, mb.provider_mailbox_id, mb.name, parent.id AS parent_id
FROM mailboxes mb
LEFT JOIN mailboxes parent ON parent.connected_account_id = mb.connected_account_id
    AND parent.provider_mailbox_id = mb.provider_parent_id
    AND parent.role IS NULL AND parent.made_by = 'member'
WHERE mb.connected_account_id = @connected_account_id AND mb.role IS NULL AND mb.made_by = 'member'
ORDER BY mb.name, mb.id;
