-- +goose Up
-- An outgoing message is one a member sends from one of their connected
-- accounts, kept with how far sending it has got; a job sends it. It belongs
-- to its account's owner, as synchronized mail does, so deleting the account,
-- or its user, deletes it. Names follow synchronized mail's.
CREATE TABLE outgoing_messages (
    -- amplop's own and random, so a URL that names the message tells nothing
    -- of how many there are, as for synchronized messages.
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connected_account_id bigint NOT NULL REFERENCES connected_accounts (id) ON DELETE CASCADE,
    -- The account's email when the message was saved, so a reconnect that
    -- changes the account's leaves this message as it was.
    from_address         text NOT NULL CHECK (char_length(from_address) BETWEEN 3 AND 254),
    -- Each a JSON array of {"name", "address"}, as synchronized messages keep
    -- theirs (JMAP's EmailAddress): addresses from emailaddress.ParseRecipient,
    -- at most 100 in each, with an empty name until the form takes names.
    to_addresses         jsonb NOT NULL CHECK (jsonb_typeof(to_addresses) = 'array'
                         AND jsonb_array_length(to_addresses) <= 100),
    cc_addresses         jsonb NOT NULL CHECK (jsonb_typeof(cc_addresses) = 'array'
                         AND jsonb_array_length(cc_addresses) <= 100),
    bcc_addresses        jsonb NOT NULL CHECK (jsonb_typeof(bcc_addresses) = 'array'
                         AND jsonb_array_length(bcc_addresses) <= 100),
    subject              text NOT NULL CHECK (char_length(subject) <= 500),
    body                 text NOT NULL CHECK (char_length(body) <= 100000), -- plain text
    -- The ID amplop gave the message, with angle brackets, sent as its
    -- X-Amplop-Message-Id and Message-ID headers. Gmail replaces the
    -- Message-ID with its own, which synchronized mail keeps as
    -- internet_message_id; the X- header stays, so a check after an
    -- unanswered send finds the message by it.
    amplop_message_id     text NOT NULL UNIQUE CHECK (amplop_message_id LIKE '<%@%>'),
    -- A reply's threading headers (RFC 5322 section 3.6.4), worked out from
    -- the answered message when the reply is saved, so sending it needs that
    -- message no more: In-Reply-To, NULL when the answered message has no
    -- Message-ID amplop may write (msgid.Writable), and References, oldest
    -- first, empty for a new message.
    in_reply_to          text CHECK (in_reply_to LIKE '<%>'),
    internet_references  text[] NOT NULL DEFAULT '{}' CHECK (cardinality(internet_references) <= 50),
    status               text NOT NULL DEFAULT 'waiting'
                         CHECK (status IN ('waiting', 'sending', 'sent', 'failed', 'unknown')),
    failure              text CHECK (failure IN ('refused', 'sending-limit', 'needs-reconnect',
                         'token-unreadable', 'deactivated', 'sender-changed', 'gave-up')),
    -- Gmail's IDs for the message, once it accepted the message, and its
    -- thread: for a reply from the account holding the answered message, the
    -- thread it is sent into, set when saved; otherwise once it is sent.
    provider_message_id  text CHECK (provider_message_id <> ''),
    provider_thread_id   text CHECK (provider_thread_id <> ''),
    created_at           timestamptz NOT NULL DEFAULT now(), -- also the Date header
    sent_at              timestamptz, -- when Gmail accepted it
    -- The copy a member sent again of this failed or unknown message; set
    -- once, so sending it again twice returns the same copy.
    sent_again_as        uuid UNIQUE REFERENCES outgoing_messages (id) ON DELETE SET NULL,
    -- When the member dismissed this failed or unknown message: it no
    -- longer waits on them, though it can still be sent again.
    dismissed_at         timestamptz,
    CONSTRAINT outgoing_messages_recipients CHECK (
        jsonb_array_length(to_addresses) + jsonb_array_length(cc_addresses)
        + jsonb_array_length(bcc_addresses) >= 1),
    CONSTRAINT outgoing_messages_sent_again_as CHECK (
        sent_again_as IS NULL OR status IN ('failed', 'unknown')),
    CONSTRAINT outgoing_messages_dismissed_at CHECK (
        dismissed_at IS NULL OR status IN ('failed', 'unknown')),
    CONSTRAINT outgoing_messages_failure CHECK ((status = 'failed') = (failure IS NOT NULL)),
    CONSTRAINT outgoing_messages_sent_at CHECK ((status = 'sent') = (sent_at IS NOT NULL)),
    CONSTRAINT outgoing_messages_provider_message_id CHECK ((status = 'sent') = (provider_message_id IS NOT NULL)),
    CONSTRAINT outgoing_messages_provider_thread_id CHECK (status <> 'sent' OR provider_thread_id IS NOT NULL)
);

-- Deleting an account finds its messages, and a member's recent messages
-- are found through their accounts.
CREATE INDEX outgoing_messages_account_created_idx ON outgoing_messages (connected_account_id, created_at);

-- +goose Down
DROP TABLE outgoing_messages;
