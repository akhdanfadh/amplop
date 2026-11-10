-- name: LockOutgoingMessageSender :one
-- The key share lock keeps the account from being deleted, and with it the
-- message about to be saved, until the transaction ends; it does not wait
-- for a token being replaced.
SELECT id, email, (needs_reconnect_since IS NOT NULL)::boolean AS needs_reconnect
FROM connected_accounts
WHERE id = @id AND user_id = @user_id
FOR KEY SHARE;

-- name: LockSendingMember :one
-- Serializes a member's sends, so the limit counts every attempt, and reads
-- whether a deactivation committed first. No key column changes, so
-- rows referencing the user need not wait.
SELECT (deactivated_at IS NOT NULL)::boolean AS deactivated
FROM users WHERE id = @user_id FOR NO KEY UPDATE;

-- name: ListSendAttemptTimes :many
SELECT created_at FROM send_attempts
WHERE user_id = @user_id AND created_at > @since;

-- name: InsertSendAttempt :exec
INSERT INTO send_attempts (user_id, created_at) VALUES (@user_id, @created_at);

-- name: DeleteOldSendAttempts :exec
-- The member's attempts no window counts any more.
DELETE FROM send_attempts WHERE user_id = @user_id AND created_at <= @since;

-- name: InsertOutgoingMessage :one
INSERT INTO outgoing_messages (connected_account_id, from_address,
    to_addresses, cc_addresses, bcc_addresses, subject, body, inbok_message_id)
VALUES (@connected_account_id, @from_address,
    @to_addresses, @cc_addresses, @bcc_addresses, @subject, @body, @inbok_message_id)
RETURNING id, status, created_at;

-- name: ReadOutgoingMessage :one
-- A message belongs to its account's owner.
SELECT om.id, om.connected_account_id, om.from_address, om.to_addresses,
    om.cc_addresses, om.bcc_addresses, om.subject, om.body,
    om.inbok_message_id, om.status, om.failure, om.provider_message_id,
    om.provider_thread_id, om.created_at, om.sent_at
FROM outgoing_messages om
JOIN connected_accounts ca ON ca.id = om.connected_account_id
WHERE om.id = @id AND ca.user_id = @user_id;

-- name: ReadOutgoingMessageToSend :one
SELECT * FROM outgoing_messages WHERE id = @id;

-- name: LockOutgoingMessage :one
-- No key column changes, so foreign key checks on the row need not wait.
SELECT * FROM outgoing_messages WHERE id = @id FOR NO KEY UPDATE;

-- name: UpdateOutgoingMessageStatus :one
UPDATE outgoing_messages SET
    status = @status,
    failure = @failure,
    provider_message_id = @provider_message_id,
    provider_thread_id = @provider_thread_id,
    sent_at = @sent_at
WHERE id = @id
RETURNING *;

-- name: UpdateConnectedAccountEmail :exec
-- Gmail's address for the account, which a send found changed.
UPDATE connected_accounts SET email = @email WHERE id = @id;

-- name: LockOutgoingMessageMember :one
-- The share lock waits for a deactivation in progress, so the member's flag
-- is read as ADR029 asks before provider work.
SELECT (u.deactivated_at IS NOT NULL)::boolean AS deactivated
FROM outgoing_messages om
JOIN connected_accounts ca ON ca.id = om.connected_account_id
JOIN users u ON u.id = ca.user_id
WHERE om.id = @id
FOR SHARE OF u;
