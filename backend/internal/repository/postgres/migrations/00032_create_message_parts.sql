-- +goose Up
-- The MIME parts of each synchronized message whose content inbok fetched:
-- one row per part of the tree, the bytes in the content store under blob_id.
-- A message has rows for all its parts or none; none means its content was
-- never fetched.
--
-- Rows are written in a transaction that first locks the account's sync
-- state, as a sync write does, so the foreign key on messages joins the
-- lock order: sync state, then message, then parts.
CREATE TABLE message_parts (
    connected_account_id bigint NOT NULL,
    message_id           uuid NOT NULL,
    -- The part's place in the tree: the index of each part on the way down
    -- from the top part, whose path is empty. Ordering by it lists the tree
    -- depth first, parents before their parts.
    path                 integer[] NOT NULL,
    -- The provider's handle for fetching the part's bytes, opaque to all
    -- but its adapter; path, not this, places the part in the tree. Gmail's
    -- partId, e.g. "0.1", its top part's possibly empty; later an IMAP body
    -- section, e.g. "1.2" for BODY[1.2] (RFC 9051 section 6.4.5), empty for
    -- a multipart top part; or a Microsoft Graph attachment's id, empty for
    -- the body.
    provider_part_id     text NOT NULL,
    media_type           text NOT NULL CHECK (media_type <> ''), -- lower case, without parameters
    charset              text NOT NULL, -- as the sender declared it, lower case; empty when none
    filename             text NOT NULL,
    -- The content's length with its transfer encoding undone, as Gmail
    -- reports it. IMAP's BODYSTRUCTURE gives the encoded length, e.g. a
    -- third more under base64, so its adapter estimates or fetches this.
    size                 bigint NOT NULL CHECK (size >= 0),
    disposition          text NOT NULL CHECK (disposition IN ('', 'inline', 'attachment')),
    mime_content_id      text NOT NULL, -- the Content-ID header, without angle brackets; empty when none
    -- For multipart/related, the Content-ID of the part shown (RFC 2387);
    -- empty when none.
    related_start        text NOT NULL,
    kind                 text NOT NULL CHECK (kind IN ('multipart', 'text', 'attachment', 'inline')),
    -- The ID the part's content is stored under, once stored: at once for
    -- a text part, when first asked for an attachment or inline part. JMAP
    -- names it blobId (RFC 8621 section 4.1.4); it is not the Content-ID
    -- header, which is mime_content_id. A row names a blob only after it
    -- was uploaded.
    blob_id              uuid UNIQUE,
    PRIMARY KEY (message_id, path),
    FOREIGN KEY (connected_account_id, message_id) REFERENCES messages (connected_account_id, id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE message_parts;
