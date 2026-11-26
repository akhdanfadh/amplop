-- name: LockOutgoingMessageSender :one
-- The key share lock keeps the account from being deleted, and with it the
-- message about to be saved, until the transaction ends; it does not wait
-- for a token being replaced.
-- The credential lets the service refuse an account whose token no key opens.
SELECT id, email, (needs_reconnect_since IS NOT NULL)::boolean AS needs_reconnect,
    refresh_token_key_id, refresh_token_ciphertext
FROM connected_accounts
WHERE id = @id AND user_id = @user_id
FOR KEY SHARE;

-- name: LockSendingMember :one
-- Serializes a member's sends, so the limit counts every attempt and a
-- repeated Idempotency-Key finds the message the first request saved, and
-- reads whether a deactivation committed first. No key column changes, so
-- rows referencing the user need not wait.
SELECT (deactivated_at IS NOT NULL)::boolean AS deactivated
FROM users WHERE id = @user_id FOR NO KEY UPDATE;

-- name: OutgoingMessageKeyExists :one
SELECT EXISTS (SELECT 1 FROM outgoing_message_keys
    WHERE user_id = @user_id AND idempotency_key = @idempotency_key);

-- name: InsertOutgoingMessageKey :exec
INSERT INTO outgoing_message_keys (user_id, idempotency_key, message_id, answered_message_id)
VALUES (@user_id, @idempotency_key, @message_id, @answered_message_id);

-- name: ReadAnsweredMessage :one
-- The synchronized message a reply answers, in one of the member's
-- accounts. Unlocked: a sync that deletes it meanwhile leaves the reply
-- with the threading read, which is all the reply needs of it.
SELECT m.connected_account_id, m.provider_thread_id, m.subject,
    m.internet_message_id, m.in_reply_to, m.internet_references
FROM messages m
JOIN connected_accounts ca ON ca.id = m.connected_account_id
WHERE m.id = @id AND ca.user_id = @user_id;

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
    to_addresses, cc_addresses, bcc_addresses, subject, body, amplop_message_id,
    in_reply_to, internet_references, provider_thread_id)
VALUES (@connected_account_id, @from_address,
    @to_addresses, @cc_addresses, @bcc_addresses, @subject, @body, @amplop_message_id,
    @in_reply_to, @internet_references, @provider_thread_id)
RETURNING id, status, created_at;

-- name: ReadOutgoingMessage :one
-- A message belongs to its account's owner.
SELECT om.id, om.connected_account_id, om.from_address, om.to_addresses,
    om.cc_addresses, om.bcc_addresses, om.subject, om.body,
    om.amplop_message_id, om.in_reply_to, om.internet_references, om.status,
    om.failure, om.provider_message_id,
    om.provider_thread_id, om.created_at, om.sent_at, om.sent_again_as,
    om.dismissed_at
FROM outgoing_messages om
JOIN connected_accounts ca ON ca.id = om.connected_account_id
WHERE om.id = @id AND ca.user_id = @user_id;

-- name: ReadOutgoingMessageKey :one
-- The message a member's key saved, and the message its request answered.
SELECT message_id, answered_message_id
FROM outgoing_message_keys
WHERE user_id = @user_id AND idempotency_key = @idempotency_key;

-- name: ListOutgoingMessages :many
-- The member's messages, without their bodies, which a list does not show
-- and which may be 100,000 characters each: the outstanding ones first,
-- waiting, sending, or failed or unknown and neither sent again nor
-- dismissed (the rule of outgoingmessage's Message.Outstanding), so newer
-- finished ones never push one out, then the rest, each newest first.
SELECT om.id, om.connected_account_id, om.from_address, om.to_addresses,
    om.cc_addresses, om.bcc_addresses, om.subject, ''::text AS body,
    om.amplop_message_id, om.in_reply_to, om.internet_references, om.status, om.failure,
    om.provider_message_id, om.provider_thread_id, om.created_at, om.sent_at,
    om.sent_again_as, om.dismissed_at
FROM outgoing_messages om
JOIN connected_accounts ca ON ca.id = om.connected_account_id
WHERE ca.user_id = @user_id
ORDER BY (om.status IN ('waiting', 'sending')
        OR (om.status IN ('failed', 'unknown') AND om.sent_again_as IS NULL
            AND om.dismissed_at IS NULL)) DESC,
    om.created_at DESC, om.id DESC
LIMIT @max_count;

-- name: SetOutgoingMessageSentAgain :exec
UPDATE outgoing_messages SET sent_again_as = @sent_again_as WHERE id = @id;

-- name: ReadOutgoingMessageToSend :one
SELECT id, connected_account_id, from_address, to_addresses, cc_addresses,
    bcc_addresses, subject, body, amplop_message_id, in_reply_to,
    internet_references, status, failure, provider_message_id,
    provider_thread_id, created_at, sent_at, sent_again_as, dismissed_at
FROM outgoing_messages WHERE id = @id;

-- name: LockOutgoingMessage :one
-- No key column changes, so foreign key checks on the row need not wait.
SELECT id, connected_account_id, from_address, to_addresses, cc_addresses,
    bcc_addresses, subject, body, amplop_message_id, in_reply_to,
    internet_references, status, failure, provider_message_id,
    provider_thread_id, created_at, sent_at, sent_again_as, dismissed_at
FROM outgoing_messages WHERE id = @id FOR NO KEY UPDATE;

-- name: UpdateOutgoingMessageStatus :one
UPDATE outgoing_messages SET
    status = @status,
    failure = @failure,
    provider_message_id = @provider_message_id,
    -- A transition without a thread keeps the one a reply was saved with.
    provider_thread_id = coalesce(sqlc.narg('provider_thread_id'), provider_thread_id),
    sent_at = @sent_at
WHERE id = @id
RETURNING id, connected_account_id, from_address, to_addresses, cc_addresses,
    bcc_addresses, subject, body, amplop_message_id, in_reply_to,
    internet_references, status, failure, provider_message_id,
    provider_thread_id, created_at, sent_at, sent_again_as, dismissed_at;

-- name: UpdateConnectedAccountEmail :exec
-- Gmail's address for the account, which a send found changed.
UPDATE connected_accounts SET email = @email WHERE id = @id;

-- name: LockOutgoingMessageAccount :exec
-- Locks the account a message is sent from, which a send step may give
-- Gmail's address. A disconnect deletes the account, then its messages, so
-- the account is locked before the message too; no key column changes, so
-- a send being saved from the account need not wait.
SELECT ca.id
FROM connected_accounts ca
JOIN outgoing_messages om ON om.connected_account_id = ca.id
WHERE om.id = @id
FOR NO KEY UPDATE OF ca;

-- name: LockOutgoingMessageMember :one
-- The share lock waits for a deactivation in progress, so the member's flag
-- is read after it, before provider work.
SELECT (u.deactivated_at IS NOT NULL)::boolean AS deactivated
FROM outgoing_messages om
JOIN connected_accounts ca ON ca.id = om.connected_account_id
JOIN users u ON u.id = ca.user_id
WHERE om.id = @id
FOR SHARE OF u;

-- name: LockMemberOutgoingMessage :one
-- A message belongs to its account's owner. No key column changes, so
-- foreign key checks on the row need not wait.
SELECT om.id, om.connected_account_id, om.from_address, om.to_addresses,
    om.cc_addresses, om.bcc_addresses, om.subject, om.body,
    om.amplop_message_id, om.in_reply_to, om.internet_references, om.status,
    om.failure, om.provider_message_id,
    om.provider_thread_id, om.created_at, om.sent_at, om.sent_again_as,
    om.dismissed_at
FROM outgoing_messages om
JOIN connected_accounts ca ON ca.id = om.connected_account_id
WHERE om.id = @id AND ca.user_id = @user_id
FOR NO KEY UPDATE OF om;

-- name: SetOutgoingMessageDismissed :one
UPDATE outgoing_messages SET dismissed_at = @dismissed_at WHERE id = @id
RETURNING id, connected_account_id, from_address, to_addresses, cc_addresses,
    bcc_addresses, subject, body, amplop_message_id, in_reply_to,
    internet_references, status, failure, provider_message_id,
    provider_thread_id, created_at, sent_at, sent_again_as, dismissed_at;
