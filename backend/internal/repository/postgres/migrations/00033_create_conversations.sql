-- +goose Up
-- The conversations the unified inbox lists (ADR031, ADR001), kept from the
-- synchronized mail.
--
-- A column marked "denormalized" holds a value derived from other rows,
-- named after the mark, so a list reads it without grouping messages. Only
-- deriveConversations writes them, in the transaction that writes the mail
-- they come from; a write to that mail anywhere else leaves them stale
-- (ADR001, "Conversations").

-- A conversation: an account's messages that share the provider's thread
-- ID, as one list row. id is inbok's own and random, as a message's, so
-- URLs can name it.
--
-- The row is made from the conversation's visible messages: those in
-- neither Spam nor Trash, nor drafts, which inbok does not show. A
-- conversation with none is made from its Spam and Trash messages; one with
-- only drafts has no row. Like messages, the row references the account's
-- sync state, so it joins the lock order of ADR001.
CREATE TABLE conversations (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connected_account_id bigint NOT NULL REFERENCES sync_states (connected_account_id) ON DELETE CASCADE,
    provider_thread_id   text NOT NULL CHECK (provider_thread_id <> ''),
    -- The row's values, each denormalized from the messages it is made from.
    -- The address lists are JSON arrays of {"name", "address"}, as messages
    -- keep them.
    latest_date          timestamptz NOT NULL, -- denormalized: the newest message's internal_date, which lists sort by
    subject              text NOT NULL, -- denormalized: the oldest message's subject, as sent
    preview              text NOT NULL, -- denormalized: the newest message's preview
    latest_from          jsonb NOT NULL CHECK (jsonb_typeof(latest_from) = 'array'), -- denormalized: the newest message's from_addresses
    participants         jsonb NOT NULL CHECK (jsonb_typeof(participants) = 'array'), -- denormalized: every message's from_addresses in date order, each address once, at most 10
    message_count        integer NOT NULL CHECK (message_count > 0), -- denormalized: how many messages
    unread               boolean NOT NULL, -- denormalized: whether a message is not seen
    flagged              boolean NOT NULL, -- denormalized: whether a message is flagged
    in_all_mail          boolean NOT NULL, -- denormalized: whether the thread has a visible message, so All mail lists it
    -- The message Star and Mark unread change. No foreign key: a sync
    -- deletes messages before it derives their conversations again, in the
    -- same transaction.
    newest_message_id    uuid NOT NULL, -- denormalized: the newest message not in Sent, else the newest
    -- Places name a conversation with its account, as memberships do.
    CONSTRAINT conversations_account_id_key UNIQUE (connected_account_id, id)
);

-- Re-deriving a thread finds its row, which keeps its ID.
CREATE UNIQUE INDEX conversations_account_thread_idx ON conversations (connected_account_id, provider_thread_id);
-- All mail and Starred, newest first, and their unread counts.
CREATE INDEX conversations_all_mail_idx ON conversations (connected_account_id, latest_date DESC, id DESC) WHERE in_all_mail;
CREATE INDEX conversations_starred_idx ON conversations (connected_account_id, latest_date DESC, id DESC) WHERE in_all_mail AND flagged;
CREATE INDEX conversations_unread_idx ON conversations (connected_account_id) WHERE in_all_mail AND unread;

-- Which mailboxes each conversation is in, so Inbox, Sent, Spam, Trash, and
-- each label list it: a mailbox holds a visible message of it, or for Spam
-- and Trash any message. Each row is denormalized from message_mailboxes.
-- latest_date and unread repeat the conversation's, written with it, so one
-- index serves a mailbox's list and its count. Deleting either side deletes
-- the row.
CREATE TABLE conversation_mailboxes (
    connected_account_id bigint NOT NULL,
    conversation_id      uuid NOT NULL,
    mailbox_id           uuid NOT NULL,
    latest_date          timestamptz NOT NULL, -- denormalized: conversations.latest_date
    unread               boolean NOT NULL, -- denormalized: conversations.unread
    PRIMARY KEY (conversation_id, mailbox_id),
    FOREIGN KEY (connected_account_id, conversation_id) REFERENCES conversations (connected_account_id, id) ON DELETE CASCADE,
    FOREIGN KEY (connected_account_id, mailbox_id) REFERENCES mailboxes (connected_account_id, id) ON DELETE CASCADE
);

-- A mailbox's list, newest first, and its unread count. The list index also
-- serves deleting a mailbox's rows, as a mailbox ID is unique.
CREATE INDEX conversation_mailboxes_list_idx ON conversation_mailboxes (mailbox_id, latest_date DESC, conversation_id DESC);
CREATE INDEX conversation_mailboxes_unread_idx ON conversation_mailboxes (mailbox_id) WHERE unread;

-- +goose Down
DROP TABLE conversation_mailboxes;
DROP TABLE conversations;
