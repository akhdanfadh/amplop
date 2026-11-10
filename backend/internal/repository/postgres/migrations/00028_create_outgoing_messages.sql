-- +goose Up
-- An outgoing message is one a member sends from one of their connected
-- accounts (US015), kept with how far sending it has got; a job sends it.
-- It belongs to its account's owner, as synchronized mail does, so deleting
-- the account, or its user, deletes it. Names follow synchronized mail's
-- (ADR001).
CREATE TABLE outgoing_messages (
    -- inbok's own and random, so a URL that names the message tells nothing
    -- of how many there are, as for synchronized messages.
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connected_account_id bigint NOT NULL REFERENCES connected_accounts (id) ON DELETE CASCADE,
    -- The account's email when the message was saved, so a reconnect that
    -- changes the account's leaves this message as it was.
    from_address         text NOT NULL CHECK (char_length(from_address) BETWEEN 3 AND 254),
    -- Each a JSON array of {"name", "address"}, as synchronized messages keep
    -- theirs (JMAP's EmailAddress): addresses from emailaddress.ParseRecipient,
    -- at most 100 in all, with an empty name until the form takes names.
    to_addresses         jsonb NOT NULL CHECK (jsonb_typeof(to_addresses) = 'array'),
    cc_addresses         jsonb NOT NULL CHECK (jsonb_typeof(cc_addresses) = 'array'),
    bcc_addresses        jsonb NOT NULL CHECK (jsonb_typeof(bcc_addresses) = 'array'),
    subject              text NOT NULL CHECK (char_length(subject) <= 500),
    body                 text NOT NULL CHECK (char_length(body) <= 100000), -- plain text
    -- The ID inbok gave the message, with angle brackets, sent as its
    -- X-Inbok-Message-Id and Message-ID headers. Gmail replaces the
    -- Message-ID with its own, which synchronized mail keeps as
    -- internet_message_id; the X- header stays, so a check after an
    -- unanswered send finds the message by it (ADR006).
    inbok_message_id     text NOT NULL UNIQUE CHECK (inbok_message_id LIKE '<%@%>'),
    status               text NOT NULL DEFAULT 'waiting'
                         CHECK (status IN ('waiting', 'sending', 'sent', 'failed', 'unknown')),
    failure              text CHECK (failure IN ('refused', 'sending-limit', 'needs-reconnect',
                         'token-unreadable', 'deactivated', 'sender-changed', 'gave-up')),
    -- Gmail's IDs for the message and its thread, once it accepted the
    -- message; a reply is sent into the thread.
    provider_message_id  text CHECK (provider_message_id <> ''),
    provider_thread_id   text CHECK (provider_thread_id <> ''),
    created_at           timestamptz NOT NULL DEFAULT now(), -- also the Date header
    sent_at              timestamptz, -- when Gmail accepted it
    CONSTRAINT outgoing_messages_recipients CHECK (
        jsonb_array_length(to_addresses) + jsonb_array_length(cc_addresses)
        + jsonb_array_length(bcc_addresses) BETWEEN 1 AND 100),
    CONSTRAINT outgoing_messages_failure CHECK ((status = 'failed') = (failure IS NOT NULL)),
    CONSTRAINT outgoing_messages_sent_at CHECK ((status = 'sent') = (sent_at IS NOT NULL)),
    CONSTRAINT outgoing_messages_provider_message_id CHECK ((status = 'sent') = (provider_message_id IS NOT NULL)),
    CONSTRAINT outgoing_messages_provider_thread_id CHECK ((status = 'sent') = (provider_thread_id IS NOT NULL))
);

-- Deleting an account finds its messages, and a member's recent messages
-- are found through their accounts.
CREATE INDEX outgoing_messages_account_created_idx ON outgoing_messages (connected_account_id, created_at);

-- +goose Down
DROP TABLE outgoing_messages;
