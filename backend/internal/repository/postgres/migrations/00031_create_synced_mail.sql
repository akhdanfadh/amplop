-- +goose Up
-- Synchronized mail (ADR001): each connected account's mailboxes, the
-- metadata of its messages, which mailboxes each message is in, and where
-- its synchronization stands. The provider stays authoritative;
-- disconnecting deletes the account's sync state, and with it the rest.
--
-- The model follows JMAP Mail (RFC 8621), which covers both IMAP folders
-- and Gmail labels: a mailbox is a named set of messages with an optional
-- role, and a message is in a set of mailboxes, usually one for a
-- folder-based provider and any number for Gmail. Read and starred are
-- flags on the message, not mailboxes. ADR001 lists where inbok departs
-- from JMAP. Commented columns are what a later provider needs, kept here
-- so the general shape stays visible; each is added with the provider that
-- fills it.

-- Where an account's synchronization stands, one row per account, saved with
-- the account. status is the full sync's, which the member sees; polls keep
-- it complete and record only their time, or their failure.
--
-- The account's mailboxes and messages reference this row, not the
-- account's. A sync write locks this row first, so the foreign key checks of
-- what it saves lock nothing it does not already hold; a disconnect locks
-- the account, then by cascade this row, then the mail. Both take rows in
-- that one order, so neither deadlocks the other. With the mail referencing
-- the account, a write holding this row waited for the account's, which a
-- disconnect held while it waited for this one.
CREATE TABLE sync_states (
    connected_account_id bigint PRIMARY KEY REFERENCES connected_accounts (id) ON DELETE CASCADE,
    status               text NOT NULL CHECK (status IN ('waiting', 'running', 'complete', 'failed')),
    -- Where the account stood at the provider when last read, which the next
    -- poll asks for changes since: text only the provider's code reads, e.g.
    -- Gmail's history ID. NULL until a full sync records one.
    position             text CHECK (position <> ''),
    sync_generation      integer NOT NULL DEFAULT 0 CHECK (sync_generation >= 0), -- the latest full sync's; 0 before the first
    -- Why the last sync, full or poll, failed, and when; NULL once one
    -- succeeds. A failed full sync always has one; a complete account keeps a
    -- failed poll's.
    failure              text CHECK (failure IN ('provider', 'token-unreadable', 'internal')),
    failed_at            timestamptz,
    -- When inbok last confirmed its copy matched the provider: a sync, full
    -- or poll, finished, or the minute's check found the position unchanged.
    last_synced_at       timestamptz,
    -- The server's time of the member's last request to sync now, which
    -- the request limit counts and a poll compares with its first read.
    sync_requested_at    timestamptz,
    CONSTRAINT sync_states_failed_failure CHECK (status <> 'failed' OR failure IS NOT NULL),
    CONSTRAINT sync_states_failure_time CHECK ((failure IS NULL) = (failed_at IS NULL))
);

-- A mailbox: a Gmail label, or later an IMAP or Outlook folder. role marks
-- the special ones by purpose, whatever their name: every attribute of
-- IANA's IMAP Mailbox Name Attributes registry that JMAP uses as a role,
-- lowercased (RFC 8621 section 2; RFC 6154, RFC 8457, RFC 9979), except
-- Subscribed, a member's setting rather than a purpose. NULL is the
-- member's own, or one the provider made for no listed purpose, e.g.
-- Gmail's inbox tabs. Unlike JMAP, two mailboxes of an account may share a
-- role, as two IMAP folders may report one special use. name is the
-- provider's, as received: a nested Gmail label is one name with slashes,
-- e.g. Work/Projects. made_by is who made it: the provider, for its own
-- purpose, e.g. Gmail's INBOX or CATEGORY_SOCIAL, or the member; the
-- labels a member sees have no role and are the member's. id is inbok's
-- own.
CREATE TABLE mailboxes (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connected_account_id bigint NOT NULL REFERENCES sync_states (connected_account_id) ON DELETE CASCADE,
    provider_mailbox_id  text NOT NULL CHECK (provider_mailbox_id <> ''), -- Gmail's label ID; later an IMAP MAILBOXID (RFC 8474) or folder name, or an Outlook folder's ID
    name                 text NOT NULL,
    role                 text CHECK (role IN ('all', 'archive', 'drafts', 'flagged', 'important', 'inbox',
                             'junk', 'memos', 'scheduled', 'sent', 'snoozed', 'trash')),
    made_by              text NOT NULL CHECK (made_by IN ('provider', 'member')),
    -- The provider's ID of the mailbox this one nests under, NULL at the
    -- top, as each provider says it: Gmail by name (Work/Projects under
    -- Work), later IMAP by its server's separator and Outlook by its
    -- parent folder's ID. name is then the mailbox's own name. Not a
    -- reference: the parent may be listed after it, or not at all.
    provider_parent_id   text CHECK (provider_parent_id <> ''),
    -- IMAP and Outlook, not yet supported:
    -- role_choice       text, -- a role the member picked, for an IMAP server without SPECIAL-USE (RFC 6154)
    -- uid_validity      bigint, -- IMAP: a change makes every cached UID of the folder stale (RFC 9051 section 2.3.1.1); 32-bit
    -- uid_next          bigint, -- IMAP: the server's prediction of the next UID; 32-bit
    -- highest_modseq    bigint, -- IMAP CONDSTORE: changes since this (RFC 7162, 63-bit)
    -- delta_token       text, -- Outlook: Microsoft Graph's delta link for the folder
    --
    -- Memberships name a mailbox with its account, so none can join two
    -- accounts' rows.
    CONSTRAINT mailboxes_account_id_key UNIQUE (connected_account_id, id)
);

-- The step that lists an account's mailboxes updates each in place, so
-- memberships keep pointing at it.
CREATE UNIQUE INDEX mailboxes_account_provider_id_idx ON mailboxes (connected_account_id, provider_mailbox_id);

-- A message's metadata as synchronization last read it, without its body,
-- which content storage keeps (ADR009). id is inbok's own and random, never
-- derived from mail, so it can name the message in search documents, object
-- keys, and URLs.
CREATE TABLE messages (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connected_account_id bigint NOT NULL REFERENCES sync_states (connected_account_id) ON DELETE CASCADE,
    -- Gmail's message ID, the same in every label; later an IMAP EMAILID
    -- (RFC 8474) or an Outlook message ID.
    provider_message_id  text NOT NULL CHECK (provider_message_id <> ''),
    -- Gmail's thread ID; later an IMAP THREADID (RFC 8474), which may be
    -- NIL, or Outlook's conversationId. Without one, inbok would thread by
    -- headers.
    provider_thread_id   text NOT NULL CHECK (provider_thread_id <> ''),
    -- The provider's own timestamp, which the window counts from: Gmail's
    -- internalDate, when Google accepted received mail, or for imported
    -- mail possibly its Date header.
    internal_date        timestamptz NOT NULL,
    subject              text NOT NULL,
    -- Each a JSON array of {"name", "address"}, decoded from the header, as
    -- JMAP's EmailAddress.
    from_addresses       jsonb NOT NULL CHECK (jsonb_typeof(from_addresses) = 'array'),
    reply_to_addresses   jsonb NOT NULL CHECK (jsonb_typeof(reply_to_addresses) = 'array'),
    to_addresses         jsonb NOT NULL CHECK (jsonb_typeof(to_addresses) = 'array'),
    cc_addresses         jsonb NOT NULL CHECK (jsonb_typeof(cc_addresses) = 'array'),
    preview              text NOT NULL, -- the provider's plain-text excerpt of the body: Gmail's snippet
    seen                 boolean NOT NULL, -- read: IMAP \Seen, JMAP $seen; Gmail's UNREAD label is its absence
    flagged              boolean NOT NULL, -- starred: IMAP \Flagged, JMAP $flagged; Gmail's STARRED label
    -- The provider's change number this state was read at, which grows
    -- with every change: Gmail's historyId, of the message or of the
    -- history record that changed it; later IMAP's MODSEQ, at most 63 bits
    -- (RFC 7162). An update read at an older number is skipped, so a late
    -- write never undoes a newer one.
    provider_version     bigint NOT NULL CHECK (provider_version > 0),
    -- answered          boolean NOT NULL, -- IMAP \Answered, JMAP $answered; Gmail does not expose it
    -- The headers a reply threads with (RFC 5322 section 3.6.4), each
    -- msg-id with its angle brackets.
    internet_message_id  text NOT NULL, -- empty when the header is missing
    in_reply_to          text[] NOT NULL,
    internet_references  text[] NOT NULL,
    -- The account's sync generation when a step, of a full sync or a poll,
    -- last saved the message. A full sync starts a generation and, once it
    -- finishes, deletes the messages of earlier ones, which the provider no
    -- longer lists. A number rather than a time, so no two clocks are
    -- compared.
    sync_generation      integer NOT NULL CHECK (sync_generation > 0),
    -- When fetching the message's content in the background last failed for
    -- good: the provider no longer had it or refused it, or its answer
    -- passed inbok's cap or could not be read (ADR009, "Fetching content").
    -- The background fetch skips the message for a day after, so one such
    -- message neither holds back the others nor is fetched every minute;
    -- opening it still tries. NULL when no fetch failed.
    body_failed_at       timestamptz,
    -- As mailboxes_account_id_key.
    CONSTRAINT messages_account_id_key UNIQUE (connected_account_id, id)
);

-- A retried sync updates the row it saved before rather than adding one.
CREATE UNIQUE INDEX messages_account_provider_id_idx ON messages (connected_account_id, provider_message_id);
-- Lists an account's messages by date, and finds its oldest.
CREATE INDEX messages_account_internal_date_idx ON messages (connected_account_id, internal_date);
-- Finds a thread's messages, which its conversation is derived from.
CREATE INDEX messages_account_thread_idx ON messages (connected_account_id, provider_thread_id);

-- Which mailboxes a message is in; none for a Gmail message with no label,
-- where JMAP would ask for at least one. Both sides are named with the
-- account, so a membership cannot join two accounts' rows, and deleting
-- either side deletes it, so a label deleted at Gmail leaves no dangling
-- ID.
CREATE TABLE message_mailboxes (
    connected_account_id bigint NOT NULL,
    message_id           uuid NOT NULL,
    mailbox_id           uuid NOT NULL,
    -- uid               bigint, -- IMAP: the message's UID in this folder; a move gives it the destination's
    PRIMARY KEY (message_id, mailbox_id),
    FOREIGN KEY (connected_account_id, message_id) REFERENCES messages (connected_account_id, id) ON DELETE CASCADE,
    FOREIGN KEY (connected_account_id, mailbox_id) REFERENCES mailboxes (connected_account_id, id) ON DELETE CASCADE
);

-- Lists a mailbox's messages, and serves deleting a mailbox.
CREATE INDEX message_mailboxes_mailbox_id_idx ON message_mailboxes (connected_account_id, mailbox_id);

-- +goose Down
DROP TABLE message_mailboxes;
DROP TABLE messages;
DROP TABLE mailboxes;
DROP TABLE sync_states;
