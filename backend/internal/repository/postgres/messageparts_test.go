package postgres

import (
	"slices"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/mailsync"
	"inbok/internal/messagecontent"
)

// messagePartsTest is a [syncedMailTest] with the accounts' mailboxes.
type messagePartsTest struct {
	syncedMailTest
}

func newMessagePartsTest(t *testing.T) messagePartsTest {
	t.Helper()
	return messagePartsTest{syncedMailTest: newSyncedMailTest(t).withMailboxes(t)}
}

// save saves messages with providerIDs to the account and returns their
// IDs, in that order.
func (s messagePartsTest) save(t *testing.T, providerIDs ...string) []uuid.UUID {
	t.Helper()
	var saved []mailsync.Message
	for _, id := range providerIDs {
		saved = append(saved, syncedMessage(id))
	}
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: saved})
	messages := s.messages(t, s.account)
	ids := make([]uuid.UUID, len(providerIDs))
	for i, id := range providerIDs {
		ids[i] = messages[id].ID
	}
	return ids
}

// storedTree is a part tree as a message's parts are saved: text parts
// stored under new IDs, the inline image and the PDF not yet, and every
// field set somewhere.
func storedTree() messagecontent.Part {
	return messagecontent.Part{ID: "", MediaType: "multipart/mixed", Kind: messagecontent.KindMultipart, Parts: []messagecontent.Part{
		{ID: "0", MediaType: "multipart/alternative", Kind: messagecontent.KindMultipart, Parts: []messagecontent.Part{
			{ID: "0.0", MediaType: "text/plain", Charset: "iso-8859-1", Size: 6, Kind: messagecontent.KindText, BlobID: uuid.New()},
			{ID: "0.1", MediaType: "multipart/related", Start: "body", Kind: messagecontent.KindMultipart, Parts: []messagecontent.Part{
				{ID: "0.1.0", MediaType: "text/html", Charset: "utf-8", Size: 12, ContentID: "body", Kind: messagecontent.KindText, BlobID: uuid.New()},
				{ID: "0.1.1", MediaType: "image/png", Size: 2048, Disposition: messagecontent.DispositionInline, ContentID: "logo", Kind: messagecontent.KindInline},
			}},
		}},
		{ID: "1", MediaType: "application/pdf", Filename: "Laporan Q3.pdf", Size: 90210, Disposition: messagecontent.DispositionAttachment, Kind: messagecontent.KindAttachment},
	}}
}

// compareUUIDs orders UUIDs by their bytes.
func compareUUIDs(a, b uuid.UUID) int {
	return slices.Compare(a[:], b[:])
}

// blobIDs returns the blob IDs root's parts name, sorted.
func blobIDs(root messagecontent.Part) []uuid.UUID {
	var ids []uuid.UUID
	var walk func(messagecontent.Part)
	walk = func(part messagecontent.Part) {
		if part.BlobID != uuid.Nil() {
			ids = append(ids, part.BlobID)
		}
		for _, child := range part.Parts {
			walk(child)
		}
	}
	walk(root)
	slices.SortFunc(ids, compareUUIDs)
	return ids
}

// A saved tree reads back whole, in order, with its blob IDs; a
// message without parts reads as none.
func TestMessagePartsSave(t *testing.T) {
	s := newMessagePartsTest(t)
	ids := s.save(t, "m1", "m2")
	tree := storedTree()

	saved, err := s.db.SaveMessageParts(t.Context(), s.account, ids[0], tree)
	require.NoError(t, err)
	assert.True(t, saved)

	got, ok, err := s.db.MessageParts(t.Context(), s.account, ids[0])
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, tree, got)

	_, ok, err = s.db.MessageParts(t.Context(), s.account, ids[1])
	require.NoError(t, err)
	assert.False(t, ok)
	_, ok, err = s.db.MessageParts(t.Context(), s.other, ids[0])
	require.NoError(t, err)
	assert.False(t, ok, "another account's message")
}

// A single part, e.g. a plain text message, is a tree of one.
func TestMessagePartsSaveSingle(t *testing.T) {
	s := newMessagePartsTest(t)
	ids := s.save(t, "m1")
	plain := messagecontent.Part{ID: "", MediaType: "text/plain", Kind: messagecontent.KindText, BlobID: uuid.New()}

	_, err := s.db.SaveMessageParts(t.Context(), s.account, ids[0], plain)
	require.NoError(t, err)

	got, ok, err := s.db.MessageParts(t.Context(), s.account, ids[0])
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, plain, got)
}

// A message's parts are saved once; a second save keeps the first.
func TestMessagePartsSaveAgain(t *testing.T) {
	s := newMessagePartsTest(t)
	ids := s.save(t, "m1")
	first := storedTree()
	_, err := s.db.SaveMessageParts(t.Context(), s.account, ids[0], first)
	require.NoError(t, err)

	saved, err := s.db.SaveMessageParts(t.Context(), s.account, ids[0], storedTree())
	require.NoError(t, err)
	assert.False(t, saved)

	got, _, err := s.db.MessageParts(t.Context(), s.account, ids[0])
	require.NoError(t, err)
	assert.Equal(t, first, got)
}

// Parts of a message the account does not have are refused.
func TestMessagePartsSaveNotFound(t *testing.T) {
	s := newMessagePartsTest(t)
	ids := s.save(t, "m1")

	for name, c := range map[string]struct {
		account int64
		message uuid.UUID
	}{
		"unknown message":         {s.account, uuid.New()},
		"another account's":       {s.other, ids[0]},
		"account without a state": {-1, ids[0]},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.db.SaveMessageParts(t.Context(), c.account, c.message, storedTree())
			assert.ErrorIs(t, err, messagecontent.ErrMessageNotFound)
		})
	}
}

// Disconnecting deletes the account's parts with its mail.
func TestMessagePartsDisconnect(t *testing.T) {
	s := newMessagePartsTest(t)
	ids := s.save(t, "m1")
	_, err := s.db.SaveMessageParts(t.Context(), s.account, ids[0], storedTree())
	require.NoError(t, err)

	_, err = s.conn.Exec(t.Context(), "DELETE FROM connected_accounts WHERE id = $1", s.account)
	require.NoError(t, err)

	var parts int
	require.NoError(t, s.conn.QueryRow(t.Context(), "SELECT count(*) FROM message_parts").Scan(&parts))
	assert.Zero(t, parts)
}

// The sweep learns which blobs no part names.
func TestMessagePartsUnused(t *testing.T) {
	s := newMessagePartsTest(t)
	ids := s.save(t, "m1")
	tree := storedTree()
	_, err := s.db.SaveMessageParts(t.Context(), s.account, ids[0], tree)
	require.NoError(t, err)
	orphan := uuid.New()

	unused, err := s.db.UnusedBlobs(t.Context(), append(blobIDs(tree), orphan))
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{orphan}, unused)

	unused, err = s.db.UnusedBlobs(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, unused)
}

// The table refuses what no part can be.
func TestMessagePartsChecks(t *testing.T) {
	s := newMessagePartsTest(t)
	ids := s.save(t, "m1")
	const insert = `INSERT INTO message_parts (connected_account_id, message_id, path, provider_part_id, media_type,
		charset, filename, size, disposition, mime_content_id, related_start, kind, blob_id)
		VALUES ($1, $2, $3, '', $4, '', '', $5, $6, '', '', $7, $8)`
	shared := uuid.New()
	_, err := s.conn.Exec(t.Context(), insert, s.account, ids[0], []int32{}, "text/plain", 0, "", "text", shared)
	require.NoError(t, err)

	tests := map[string]struct {
		account     int64
		mediaType   string
		size        int64
		disposition string
		kind        string
		content     *uuid.UUID
	}{
		"empty media type":    {s.account, "", 0, "", "text", nil},
		"negative size":       {s.account, "text/plain", -1, "", "text", nil},
		"unknown disposition": {s.account, "text/plain", 0, "hidden", "text", nil},
		"unknown kind":        {s.account, "text/plain", 0, "", "other", nil},
		"shared content":      {s.account, "text/plain", 0, "", "text", &shared},
		"another account":     {s.other, "text/plain", 0, "", "text", nil},
	}
	index := int32(0)
	for name, c := range tests {
		// Each case its own path, so only its fault can refuse it.
		index++
		path := []int32{index}
		t.Run(name, func(t *testing.T) {
			_, err := s.conn.Exec(t.Context(), insert, c.account, ids[0], path, c.mediaType, c.size, c.disposition, c.kind, c.content)
			require.Error(t, err)
			_, err = s.conn.Exec(t.Context(), insert, s.account, ids[0], path, "text/plain", 0, "", "text", nil)
			assert.NoError(t, err, "the same row without its fault")
		})
	}
}
