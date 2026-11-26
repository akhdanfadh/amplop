package google

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/gmail/v1"

	"amplop/internal/messagecontent"
)

// The fixtures in testdata are messages.get responses in the full format,
// built in the shape of a recorded one: the top part's partId is empty,
// every part carries its own header fields in the sender's case, e.g.
// Content-Id, multiparts have a zero-size body, data is base64url with or
// without padding, a text part's data is UTF-8 whatever charset it
// declares while its size is the length as sent, and files and inline
// images come as an attachmentId. charsets first held data in the declared
// charsets, which the recordings in testdata/recorded showed Gmail never
// sends. No generator is kept: edit the JSON.

// parsed returns the part tree and text of the fixture in testdata/name.
func parsed(t *testing.T, name string) (messagecontent.Part, []messagecontent.Text) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	require.NoError(t, err)
	var message gmail.Message
	require.NoError(t, json.Unmarshal(raw, &message))
	received, err := receivedPart(message.Payload)
	require.NoError(t, err)
	tree, texts, err := messagecontent.Parse(received)
	require.NoError(t, err)
	return tree, texts
}

// texts returns texts as part ID to text.
func texts(texts []messagecontent.Text) map[string]string {
	out := make(map[string]string, len(texts))
	for _, text := range texts {
		out[text.PartID] = string(text.Content)
	}
	return out
}

// ids returns the IDs of parts.
func ids(parts []messagecontent.Part) []string {
	out := []string{}
	for _, part := range parts {
		out = append(out, part.ID)
	}
	return out
}

func TestReceivedPartTree(t *testing.T) {
	tree, got := parsed(t, "related-in-alternative-in-mixed")

	want := messagecontent.Part{
		ID: "", MediaType: "multipart/mixed", Kind: messagecontent.KindMultipart,
		Parts: []messagecontent.Part{
			{
				ID: "0", MediaType: "multipart/alternative", Kind: messagecontent.KindMultipart,
				Parts: []messagecontent.Part{
					{ID: "0.0", MediaType: "text/plain", Charset: "utf-8", Size: 28, Kind: messagecontent.KindText},
					{
						ID: "0.1", MediaType: "multipart/related", Kind: messagecontent.KindMultipart,
						Parts: []messagecontent.Part{
							{ID: "0.1.0", MediaType: "text/html", Charset: "utf-8", Size: 167, Kind: messagecontent.KindText},
							{
								ID: "0.1.1", MediaType: "image/png", Filename: "logo.png", Size: 24388,
								Disposition: messagecontent.DispositionInline, ContentID: "logo.F0B535DB@example.org",
								Kind: messagecontent.KindInline,
							},
						},
					},
				},
			},
			{ID: "1", MediaType: "application/pdf", Filename: "report.pdf", Size: 934, Disposition: messagecontent.DispositionAttachment, Kind: messagecontent.KindAttachment},
			{ID: "2", MediaType: "text/plain", Filename: "cert.pem", Size: 2952, Disposition: messagecontent.DispositionAttachment, Kind: messagecontent.KindAttachment},
		},
	}
	assert.Equal(t, want, tree)
	assert.Equal(t, map[string]string{
		"0.0":   "Hello, here is the report.\r\n",
		"0.1.0": `<html><body><p>Hello, here is the <font color="#ff0000">report</font>.</p><img src="cid:logo.F0B535DB@example.org" alt="logo" width="120" height="120"></body></html>` + "\r\n",
	}, texts(got))
	shown := messagecontent.Choose(tree)
	assert.Equal(t, []string{"0.1.0"}, ids(shown.Bodies))
	assert.Equal(t, []string{"1", "2"}, ids(shown.Attachments))
}

// Each fixture parses to the text and display its shape calls for.
func TestReceivedPartFixtures(t *testing.T) {
	tests := []struct {
		fixture         string
		wantTexts       map[string]string
		wantBodies      []string
		wantAttachments []string
	}{
		{
			fixture:         "plain",
			wantTexts:       map[string]string{"": "Just a line of plain text.\r\n"},
			wantBodies:      []string{""},
			wantAttachments: []string{},
		},
		{
			fixture:         "empty",
			wantTexts:       map[string]string{"": ""},
			wantBodies:      []string{""},
			wantAttachments: []string{},
		},
		{
			fixture:         "attachment-without-filename",
			wantTexts:       map[string]string{"0": "See the file.\r\n"},
			wantBodies:      []string{"0"},
			wantAttachments: []string{"1"},
		},
		{
			fixture: "charsets",
			wantTexts: map[string]string{
				"0": "Café crème\r\n",
				"1": `<meta charset="windows-1251"><p>Привет</p>`,
				"2": "naïve\r\n",
				"3": "£100\r\n",
				"4": "こんにちは\r\n",
			},
			wantBodies:      []string{"0", "1", "2", "3", "4"},
			wantAttachments: []string{},
		},
		{
			// The HTML comes through messages.attachments.get, so only the
			// plain text is here, though the view shows the HTML.
			fixture:         "html-fetched-separately",
			wantTexts:       map[string]string{"0": "Weekly digest, plain version.\r\n"},
			wantBodies:      []string{"1"},
			wantAttachments: []string{},
		},
		{
			// The forwarded message's own text is never shown, so not kept.
			fixture:         "forwarded-message",
			wantTexts:       map[string]string{"0": "Forwarding this one.\r\n"},
			wantBodies:      []string{"0"},
			wantAttachments: []string{"1"},
		},
		{
			fixture: "inline-image-in-mixed",
			wantTexts: map[string]string{
				"0": "Before the photo.\r\n",
				"2": "After the photo.\r\n",
			},
			wantBodies:      []string{"0", "2"},
			wantAttachments: []string{"1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			tree, got := parsed(t, tt.fixture)

			assert.Equal(t, tt.wantTexts, texts(got))
			shown := messagecontent.Choose(tree)
			assert.Equal(t, tt.wantBodies, ids(shown.Bodies), "bodies")
			assert.Equal(t, tt.wantAttachments, ids(shown.Attachments), "attachments")
		})
	}
}

// A part's size is Gmail's, the content's decoded length.
func TestReceivedPartSize(t *testing.T) {
	tree, got := parsed(t, "plain")

	require.Len(t, got, 1)
	assert.Equal(t, int64(len(got[0].Content)), tree.Size)
}

func TestReceivedPartData(t *testing.T) {
	tests := []struct {
		name string
		body *gmail.MessagePartBody
		want []byte
	}{
		{"padded", &gmail.MessagePartBody{Data: "aGk-Pz8=", Size: 5}, []byte("hi>??")},
		{"unpadded", &gmail.MessagePartBody{Data: "aGk-Pz8", Size: 5}, []byte("hi>??")},
		{"url alphabet", &gmail.MessagePartBody{Data: "-_8", Size: 2}, []byte{0xfb, 0xff}},
		{"no data", &gmail.MessagePartBody{}, []byte{}},
		{"fetched separately", &gmail.MessagePartBody{AttachmentId: "ANGjdJ8", Size: 900}, nil},
		{"no body", nil, []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received, err := receivedPart(&gmail.MessagePart{MimeType: "text/plain", Body: tt.body})

			require.NoError(t, err)
			assert.Equal(t, tt.want, received.Content)
		})
	}
}

// Data that is not base64url fails the whole message, naming the part.
func TestReceivedPartBadData(t *testing.T) {
	message := &gmail.MessagePart{
		MimeType: "multipart/mixed",
		Parts: []*gmail.MessagePart{
			{PartId: "0", MimeType: "text/plain", Body: &gmail.MessagePartBody{Data: "aGk+Pz8="}},
		},
	}

	_, err := receivedPart(message)

	require.ErrorContains(t, err, `part "0"`)
}

// Header fields keep every value, read in any case.
func TestReceivedPartHeader(t *testing.T) {
	received, err := receivedPart(&gmail.MessagePart{
		MimeType: "image/png",
		Headers: []*gmail.MessagePartHeader{
			{Name: "Content-Id", Value: "<a>"},
			{Name: "received", Value: "first"},
			{Name: "Received", Value: "second"},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "<a>", received.Header.Get("Content-ID"))
	assert.Equal(t, []string{"first", "second"}, received.Header.Values("Received"))
}

// A message without its payload is an error, not a panic.
func TestReceivedPartMissing(t *testing.T) {
	_, err := receivedPart(nil)
	require.ErrorIs(t, err, errNoPart)

	_, err = receivedPart(&gmail.MessagePart{MimeType: "multipart/mixed", Parts: []*gmail.MessagePart{nil}})
	require.ErrorIs(t, err, errNoPart)
}

// The fixtures in testdata/recorded are messages.get responses in the full
// format, and one messages.attachments.get response, that real Gmail gave for
// test messages inserted into a Gmail account made for testing (2026-11-12),
// with the IDs, the account's address, the project number, and the
// attachmentIds replaced. Gmail converts a text part's inline data to UTF-8
// from its declared charset, its <meta> charset, or else Latin-1, while
// keeping its Content-Type and size as sent; sends a long text part, and
// every file and inline image however small, by attachmentId, as sent;
// decodes RFC 2047 and RFC 2231 file names, rewriting the part's header
// fields to match; and nests a forwarded message's parts under its
// message/rfc822 part. The line break before a boundary belongs to the
// boundary (RFC 2046, section 5.1.1), so a body ends in a line break only
// when its content has one of its own.
func TestReceivedPartRecorded(t *testing.T) {
	tests := []struct {
		fixture         string
		wantTexts       map[string]string
		wantBodies      []string
		wantAttachments []string
		wantFilenames   map[string]string
	}{
		{
			fixture: "recorded/latin1-quoted-printable",
			wantTexts: map[string]string{
				"0": "Café crème à midi. Prix: 12 EUR.\r\nLigne longue " + strings.Repeat("x", 80) + " fin.\r\n",
				"1": "<p>Café “sérieux” – à bientôt</p>\r\n",
			},
			wantBodies:      []string{"1"},
			wantAttachments: []string{},
		},
		{
			fixture: "recorded/undeclared-charsets",
			wantTexts: map[string]string{
				"0": `<meta charset="windows-1251"><p>Привет</p>` + "\n",
				"1": "Café sans charset.\n",
				"2": "naïve us-ascii.\n",
			},
			wantBodies:      []string{"0", "1", "2"},
			wantAttachments: []string{},
		},
		{
			// The long HTML comes by attachmentId, so only the plain text
			// is here, though the view shows the HTML.
			fixture:         "recorded/long-latin1-html",
			wantTexts:       map[string]string{"0": "Résumé court.\r\n"},
			wantBodies:      []string{"1"},
			wantAttachments: []string{},
		},
		{
			// The forwarded message's own text is never shown, so not kept.
			fixture:         "recorded/forwarded-as-attachment",
			wantTexts:       map[string]string{"0": "See the forwarded agenda."},
			wantBodies:      []string{"0"},
			wantAttachments: []string{"1"},
			wantFilenames:   map[string]string{"1": "Original agenda.eml"},
		},
		{
			fixture:         "recorded/encoded-file-names",
			wantTexts:       map[string]string{"0": "Three attachments with non-ASCII names."},
			wantBodies:      []string{"0"},
			wantAttachments: []string{"1", "2", "3"},
			wantFilenames:   map[string]string{"1": "Laporan Q3 – é.pdf", "2": "Rapport été.pdf", "3": "Résumé.pdf"},
		},
		{
			fixture:         "recorded/latin1-text-file",
			wantTexts:       map[string]string{"0": "A Latin-1 text file is attached."},
			wantBodies:      []string{"0"},
			wantAttachments: []string{"1"},
			wantFilenames:   map[string]string{"1": "notes.txt"},
		},
		{
			fixture:         "recorded/inline-images",
			wantTexts:       map[string]string{"0": `<p>Logo <img src="cid:logo@example.net"> and chart <img src="cid:chart@example.net"></p>`},
			wantBodies:      []string{"0"},
			wantAttachments: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			tree, got := parsed(t, tt.fixture)

			assert.Equal(t, tt.wantTexts, texts(got))
			shown := messagecontent.Choose(tree)
			assert.Equal(t, tt.wantBodies, ids(shown.Bodies), "bodies")
			assert.Equal(t, tt.wantAttachments, ids(shown.Attachments), "attachments")
			for id, name := range tt.wantFilenames {
				assert.Equal(t, name, partByID(tree, id).Filename, "part %q", id)
			}
		})
	}
}

// A recorded text part keeps the charset its sender declared, and its size
// as sent, though its data came converted to UTF-8.
func TestReceivedPartRecordedCharset(t *testing.T) {
	tree, _ := parsed(t, "recorded/latin1-quoted-printable")

	plain := partByID(tree, "0")
	assert.Equal(t, "iso-8859-1", plain.Charset)
	assert.Equal(t, int64(134), plain.Size)
}

// Both recorded inline images are inline parts by their Content-ID, the
// larger with a file name and no disposition, and both come by
// attachmentId.
func TestReceivedPartRecordedInline(t *testing.T) {
	tree, _ := parsed(t, "recorded/inline-images")

	logo, chart := partByID(tree, "1"), partByID(tree, "2")
	assert.Equal(t, messagecontent.KindInline, logo.Kind)
	assert.Equal(t, "logo@example.net", logo.ContentID)
	assert.Equal(t, messagecontent.KindInline, chart.Kind)
	assert.Equal(t, "chart.png", chart.Filename)
	assert.Equal(t, int64(300008), chart.Size)
}

// partByID returns the part of root, or root itself, with id, or the zero
// part when there is none.
func partByID(root messagecontent.Part, id string) messagecontent.Part {
	if root.ID == id {
		return root
	}
	for _, child := range root.Parts {
		if found := partByID(child, id); found.ID == id {
			return found
		}
	}
	return messagecontent.Part{}
}
