package messagecontent

import (
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parsed returns the part tree [Parse] reads from received.
func parsed(t *testing.T, received Received) Part {
	t.Helper()
	tree, _, err := Parse(received)
	require.NoError(t, err)
	return tree
}

// header returns a MIME header holding fields, given as name, value pairs.
func header(fields ...string) textproto.MIMEHeader {
	h := textproto.MIMEHeader{}
	for i := 0; i+1 < len(fields); i += 2 {
		h.Add(fields[i], fields[i+1])
	}
	return h
}

// [Parse] keeps the tree's shape and IDs, and returns the text of each text
// part it carries, in tree order.
func TestParse(t *testing.T) {
	root := Received{
		ID:        "",
		MediaType: "multipart/mixed",
		Parts: []Received{
			{
				ID:        "0",
				MediaType: "multipart/alternative",
				Parts: []Received{
					{ID: "0.0", MediaType: "text/plain", Header: header("Content-Type", "text/plain; charset=UTF-8"), Size: 5, Content: []byte("Hello")},
					{ID: "0.1", MediaType: "text/html", Header: header("Content-Type", `text/html; charset="utf-8"`), Size: 12, Content: []byte("<p>Hello</p>")},
				},
			},
			{
				ID:        "1",
				MediaType: "application/pdf",
				Filename:  "report.pdf",
				Header:    header("Content-Type", `application/pdf; name="report.pdf"`, "Content-Disposition", `attachment; filename="report.pdf"`),
				Size:      2048,
			},
		},
	}

	tree, texts, err := Parse(root)
	require.NoError(t, err)

	want := Part{
		ID: "", MediaType: "multipart/mixed", Kind: KindMultipart,
		Parts: []Part{
			{
				ID: "0", MediaType: "multipart/alternative", Kind: KindMultipart,
				Parts: []Part{
					{ID: "0.0", MediaType: "text/plain", Charset: "utf-8", Size: 5, Kind: KindText},
					{ID: "0.1", MediaType: "text/html", Charset: "utf-8", Size: 12, Kind: KindText},
				},
			},
			{ID: "1", MediaType: "application/pdf", Filename: "report.pdf", Size: 2048, Disposition: DispositionAttachment, Kind: KindAttachment},
		},
	}
	assert.Equal(t, want, tree)
	assert.Equal(t, []Text{{PartID: "0.0", Content: []byte("Hello")}, {PartID: "0.1", Content: []byte("<p>Hello</p>")}}, texts)
}

// A text part whose content the provider sends separately has no [Text];
// an empty one has empty text.
func TestParseTextContent(t *testing.T) {
	root := Received{
		MediaType: "multipart/alternative",
		Parts: []Received{
			{ID: "0", MediaType: "text/plain", Content: []byte{}},
			{ID: "1", MediaType: "text/html", Size: 90000},
		},
	}

	_, texts, err := Parse(root)
	require.NoError(t, err)

	assert.Equal(t, []Text{{PartID: "0", Content: []byte{}}}, texts)
}

// Only a text part's content is returned, never an attachment's.
func TestParseAttachmentContent(t *testing.T) {
	root := Received{MediaType: "text/plain", Filename: "notes.txt", Content: []byte("secret")}

	tree, texts, err := Parse(root)
	require.NoError(t, err)

	assert.Equal(t, KindAttachment, tree.Kind)
	assert.Empty(t, texts)
}

func TestParseKind(t *testing.T) {
	tests := []struct {
		name     string
		received Received
		want     Kind
	}{
		{"plain text", Received{MediaType: "text/plain"}, KindText},
		{"html", Received{MediaType: "text/html"}, KindText},
		{"inline text", Received{MediaType: "text/plain", Header: header("Content-Disposition", "inline")}, KindText},
		{"upper case type", Received{MediaType: "TEXT/HTML"}, KindText},
		{"no type", Received{}, KindText},
		{"type only in header", Received{Header: header("Content-Type", "image/png")}, KindAttachment},
		{"type in header with bad parameter", Received{Header: header("Content-Type", "application/pdf; name=Foo Bar.pdf")}, KindAttachment},
		{"type not ascii", Received{MediaType: "text/pla\u0130n"}, KindText},
		{"text marked attachment", Received{MediaType: "text/plain", Header: header("Content-Disposition", "attachment")}, KindAttachment},
		{"text with filename", Received{MediaType: "text/html", Filename: "page.html"}, KindAttachment},
		{"other text type", Received{MediaType: "text/calendar"}, KindAttachment},
		{"amp html", Received{MediaType: "text/x-amp-html"}, KindAttachment},
		{"image", Received{MediaType: "image/png"}, KindAttachment},
		{"image with content id", Received{MediaType: "image/png", Header: header("Content-ID", "<logo@example.com>")}, KindInline},
		{"inline image", Received{MediaType: "image/png", Filename: "logo.png", Header: header("Content-ID", "<logo>", "Content-Disposition", "inline")}, KindInline},
		{"svg with content id", Received{MediaType: "image/svg+xml", Header: header("Content-ID", "<s>")}, KindInline},
		{"html file with content id", Received{MediaType: "text/html", Filename: "page.html", Header: header("Content-ID", "<p>")}, KindAttachment},
		{"pdf with content id", Received{MediaType: "application/pdf", Header: header("Content-ID", "<f>")}, KindAttachment},
		{"calendar with content id", Received{MediaType: "text/calendar", Header: header("Content-ID", "<c>")}, KindAttachment},
		{"content id marked attachment", Received{MediaType: "image/png", Header: header("Content-ID", "<logo>", "Content-Disposition", "attachment")}, KindAttachment},
		{"text with content id", Received{MediaType: "text/plain", Header: header("Content-ID", "<t>")}, KindText},
		{"forwarded message", Received{MediaType: "message/rfc822"}, KindAttachment},
		{"multipart", Received{MediaType: "multipart/signed"}, KindMultipart},
		{"empty multipart", Received{MediaType: "multipart/mixed"}, KindMultipart},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree := parsed(t, tt.received)
			assert.Equal(t, tt.want, tree.Kind)
		})
	}
}

func TestParseDisposition(t *testing.T) {
	tests := []struct {
		value string
		want  Disposition
	}{
		{"", DispositionNone},
		{"  ", DispositionNone},
		{"inline", DispositionInline},
		{`INLINE; filename="a.png"`, DispositionInline},
		{"attachment", DispositionAttachment},
		{`attachment; filename="a b.pdf"`, DispositionAttachment},
		{"attachment; filename=a.pdf; filename=b.pdf", DispositionAttachment},
		{"form-data", DispositionAttachment},
		{"not a type at all", DispositionAttachment},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			tree := parsed(t, Received{MediaType: "image/png", Header: header("Content-Disposition", tt.value)})
			assert.Equal(t, tt.want, tree.Disposition)
		})
	}
}

func TestParseCharset(t *testing.T) {
	tests := []struct {
		contentType string
		want        string
	}{
		{"", ""},
		{"text/plain", ""},
		{"text/plain; charset=UTF-8", "utf-8"},
		{`text/plain; charset="ISO-8859-1"`, "iso-8859-1"},
		{"text/plain; format=flowed; charset=us-ascii", "us-ascii"},
		{"text/plain; charset=x-unknown", "x-unknown"},
		{"text/plain; charset=utf-8; charset=latin1", "utf-8"},
		{"text/plain; charset=iso-8859-2; name=Foo Bar.txt", "iso-8859-2"},
		{`text/plain; name=Foo Bar.txt; CHARSET="KOI8-R"`, "koi8-r"},
		{"text/plain; charset=utf-8 format=flowed", "utf-8"},
		{"text/plain; charset=utf-8;;", "utf-8"},
		{"text/plain; charset", ""},
	}
	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			tree := parsed(t, Received{MediaType: "text/plain", Header: header("Content-Type", tt.contentType)})
			assert.Equal(t, tt.want, tree.Charset)
		})
	}
}

func TestParseContentID(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"", ""},
		{"<logo@example.com>", "logo@example.com"},
		{" <ii_k1x2> ", "ii_k1x2"},
		{"logo@example.com", "logo@example.com"},
		{"<>", ""},
		{"<unclosed", "<unclosed"},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			tree := parsed(t, Received{MediaType: "image/png", Header: header("Content-ID", tt.value)})
			assert.Equal(t, tt.want, tree.ContentID)
		})
	}
}

// A provider's header field names may come in any case.
func TestParseHeaderCase(t *testing.T) {
	h := textproto.MIMEHeader{}
	h.Add("content-id", "<logo>")
	h.Add("CONTENT-DISPOSITION", "inline")

	tree := parsed(t, Received{MediaType: "image/png", Header: h})

	require.Equal(t, KindInline, tree.Kind)
	assert.Equal(t, DispositionInline, tree.Disposition)
}

// A text part inside an attachment, e.g. a forwarded message, is never
// shown, so [Parse] keeps no text for it.
func TestParseTextInsideAttachment(t *testing.T) {
	root := Received{
		ID:        "",
		MediaType: "multipart/mixed",
		Parts: []Received{
			{ID: "0", MediaType: "text/plain", Content: []byte("Forwarding.")},
			{
				ID:        "1",
				MediaType: "message/rfc822",
				Parts: []Received{
					{ID: "1.0", MediaType: "text/plain", Content: []byte("Original.")},
				},
			},
		},
	}

	tree, texts, err := Parse(root)

	require.NoError(t, err)
	assert.Equal(t, KindText, tree.Parts[1].Parts[0].Kind)
	assert.Equal(t, []Text{{PartID: "0", Content: []byte("Forwarding.")}}, texts)
}

func TestParseTooManyParts(t *testing.T) {
	root := Received{MediaType: "multipart/mixed", Parts: make([]Received, maxParts)}

	_, _, err := Parse(root)

	require.ErrorIs(t, err, ErrTooManyParts)
}

func TestParseMostParts(t *testing.T) {
	root := Received{MediaType: "multipart/mixed", Parts: make([]Received, maxParts-1)}

	tree, _, err := Parse(root)

	require.NoError(t, err)
	assert.Len(t, tree.Parts, maxParts-1)
}

// Strings from the sender lose what PostgreSQL refuses or what disguises
// a file name.
func TestParseClean(t *testing.T) {
	root := Received{
		ID:        "0\x00",
		MediaType: "image/png",
		Filename:  "a\x00b\r\n\u202Egpj.exe\xff",
		Header:    header("Content-ID", "<lo\x00go\u2066>", "Content-Type", "image/png; charset=\"ut\x01f-8\""),
	}

	tree := parsed(t, root)

	assert.Equal(t, "0", tree.ID)
	assert.Equal(t, "abgpj.exe\uFFFD", tree.Filename)
	assert.Equal(t, "logo", tree.ContentID)
}
