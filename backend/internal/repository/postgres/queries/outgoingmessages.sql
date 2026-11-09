-- name: LockOutgoingMessageSender :one
-- The key share lock keeps the account from being deleted, and with it the
-- message about to be saved, until the transaction ends; it does not wait
-- for a token being replaced.
SELECT id, email, (needs_reconnect_since IS NOT NULL)::boolean AS needs_reconnect
FROM connected_accounts
WHERE id = @id AND user_id = @user_id
FOR KEY SHARE;

-- name: InsertOutgoingMessage :one
INSERT INTO outgoing_messages (connected_account_id, from_address,
    to_addresses, cc_addresses, bcc_addresses, subject, body, internet_message_id)
VALUES (@connected_account_id, @from_address,
    @to_addresses, @cc_addresses, @bcc_addresses, @subject, @body, @internet_message_id)
RETURNING id, status, created_at;

-- name: ReadOutgoingMessage :one
-- A message belongs to its account's owner.
SELECT om.id, om.connected_account_id, om.from_address, om.to_addresses,
    om.cc_addresses, om.bcc_addresses, om.subject, om.body,
    om.internet_message_id, om.status, om.failure, om.provider_message_id,
    om.provider_thread_id, om.created_at, om.sent_at
FROM outgoing_messages om
JOIN connected_accounts ca ON ca.id = om.connected_account_id
WHERE om.id = @id AND ca.user_id = @user_id;
