package messagecontent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// plainPart returns a plain text part for [Choose] tests.
func plainPart(id string) Part {
	return Part{ID: id, MediaType: mediaPlain, Kind: KindText}
}

// htmlPart returns an HTML text part for [Choose] tests.
func htmlPart(id string) Part {
	return Part{ID: id, MediaType: mediaHTML, Kind: KindText}
}

// filePart returns an attachment for [Choose] tests.
func filePart(id string) Part {
	return Part{ID: id, MediaType: "application/pdf", Kind: KindAttachment}
}

// imagePart returns an inline part for [Choose] tests.
func imagePart(id string) Part {
	return Part{ID: id, MediaType: "image/png", ContentID: id, Kind: KindInline}
}

// multipart returns a multipart holding parts for [Choose] tests.
func multipart(mediaType, id string, parts ...Part) Part {
	return Part{ID: id, MediaType: mediaType, Kind: KindMultipart, Parts: parts}
}

func TestChoose(t *testing.T) {
	tests := []struct {
		name            string
		root            Part
		wantBodies      []string
		wantAttachments []string
		wantInline      []string
	}{
		{
			name:       "plain text only",
			root:       plainPart(""),
			wantBodies: []string{""},
		},
		{
			name:            "attachment only",
			root:            filePart(""),
			wantAttachments: []string{""},
		},
		{
			name:       "html over plain",
			root:       multipart(mediaAlternative, "", plainPart("0"), htmlPart("1")),
			wantBodies: []string{"1"},
		},
		{
			name:       "html first",
			root:       multipart(mediaAlternative, "", htmlPart("0"), plainPart("1")),
			wantBodies: []string{"0"},
		},
		{
			name:       "last of two html",
			root:       multipart(mediaAlternative, "", plainPart("0"), htmlPart("1"), htmlPart("2")),
			wantBodies: []string{"2"},
		},
		{
			name:       "plain when no html",
			root:       multipart(mediaAlternative, "", plainPart("0"), filePart("1")),
			wantBodies: []string{"0"},
		},
		{
			name:       "amp and calendar left out",
			root:       multipart(mediaAlternative, "", plainPart("0"), htmlPart("1"), Part{ID: "2", MediaType: "text/x-amp-html", Kind: KindAttachment}),
			wantBodies: []string{"1"},
		},
		{
			name:            "alternative with nothing to show",
			root:            multipart(mediaAlternative, "", filePart("0"), filePart("1")),
			wantAttachments: []string{"1"},
		},
		{
			name: "alternative inside mixed",
			root: multipart("multipart/mixed", "",
				multipart(mediaAlternative, "0", plainPart("0.0"), htmlPart("0.1")),
				filePart("1"),
			),
			wantBodies:      []string{"0.1"},
			wantAttachments: []string{"1"},
		},
		{
			name: "related html with inline image",
			root: multipart(mediaAlternative, "",
				plainPart("0"),
				multipart(mediaRelated, "1", htmlPart("1.0"), imagePart("1.1")),
			),
			wantBodies: []string{"1.0"},
			wantInline: []string{"1.1"},
		},
		{
			name: "attachment inside the chosen alternative",
			root: multipart(mediaAlternative, "",
				plainPart("0"),
				multipart("multipart/mixed", "1", htmlPart("1.0"), filePart("1.1"), htmlPart("1.2")),
			),
			wantBodies:      []string{"1.0", "1.2"},
			wantAttachments: []string{"1.1"},
		},
		{
			name: "html after plain inside the chosen alternative",
			root: multipart(mediaAlternative, "",
				plainPart("0"),
				multipart("multipart/mixed", "1", plainPart("1.0"), htmlPart("1.1")),
			),
			wantBodies: []string{"1.0", "1.1"},
		},
		{
			name: "related with start",
			root: Part{
				MediaType: mediaRelated, Kind: KindMultipart, Start: "page",
				Parts: []Part{
					imagePart("0"),
					{ID: "1", MediaType: mediaHTML, ContentID: "page", Kind: KindText},
					filePart("2"),
				},
			},
			wantBodies:      []string{"1"},
			wantAttachments: []string{"2"},
			wantInline:      []string{"0"},
		},
		{
			name: "related with start keeps message order",
			root: Part{
				MediaType: mediaRelated, Kind: KindMultipart, Start: "page",
				Parts: []Part{
					filePart("0"),
					{
						ID: "1", MediaType: "multipart/mixed", ContentID: "page", Kind: KindMultipart,
						Parts: []Part{htmlPart("1.0"), filePart("1.1")},
					},
					filePart("2"),
				},
			},
			wantBodies:      []string{"1.0"},
			wantAttachments: []string{"0", "1.1", "2"},
		},
		{
			name: "related with start naming no part",
			root: Part{
				MediaType: mediaRelated, Kind: KindMultipart, Start: "missing",
				Parts: []Part{htmlPart("0"), imagePart("1")},
			},
			wantBodies: []string{"0"},
			wantInline: []string{"1"},
		},
		{
			name:            "related with plain root offers its images",
			root:            multipart(mediaRelated, "", plainPart("0"), imagePart("1")),
			wantBodies:      []string{"0"},
			wantAttachments: []string{"1"},
		},
		{
			name: "related offers its other text parts",
			root: multipart(mediaRelated, "",
				htmlPart("0"),
				plainPart("1"),
				multipart(mediaAlternative, "2", plainPart("2.0"), htmlPart("2.1")),
				imagePart("3"),
			),
			wantBodies:      []string{"0"},
			wantAttachments: []string{"1", "2.0", "2.1"},
			wantInline:      []string{"3"},
		},
		{
			name: "related's other html parts pick no alternative",
			root: multipart(mediaAlternative, "",
				multipart(mediaRelated, "0", plainPart("0.0"), htmlPart("0.1")),
				plainPart("1"),
			),
			wantBodies: []string{"1"},
		},
		{
			name: "attachment inside an alternative not chosen",
			root: multipart(mediaAlternative, "",
				multipart("multipart/mixed", "0", plainPart("0.0"), filePart("0.1")),
				htmlPart("1"),
			),
			wantBodies: []string{"1"},
		},
		{
			name: "related with alternative root",
			root: multipart(mediaRelated, "",
				multipart(mediaAlternative, "0", plainPart("0.0"), htmlPart("0.1")),
				imagePart("1"),
				filePart("2"),
			),
			wantBodies:      []string{"0.1"},
			wantAttachments: []string{"2"},
			wantInline:      []string{"1"},
		},
		{
			name: "texts and image in mixed",
			root: multipart("multipart/mixed", "",
				plainPart("0"),
				imagePart("1"),
				plainPart("2"),
			),
			wantBodies:      []string{"0", "2"},
			wantAttachments: []string{"1"},
		},
		{
			name: "signed reads as mixed",
			root: multipart("multipart/signed", "",
				plainPart("0"),
				Part{ID: "1", MediaType: "application/pkcs7-signature", Kind: KindAttachment},
			),
			wantBodies:      []string{"0"},
			wantAttachments: []string{"1"},
		},
		{
			name: "forwarded message stays an attachment",
			root: multipart("multipart/mixed", "",
				plainPart("0"),
				Part{ID: "1", MediaType: "message/rfc822", Kind: KindAttachment, Parts: []Part{plainPart("1.0")}},
			),
			wantBodies:      []string{"0"},
			wantAttachments: []string{"1"},
		},
		{
			name: "unknown kinds are downloads",
			root: multipart("multipart/mixed", "",
				plainPart("0"),
				Part{ID: "1", MediaType: "application/pdf"},
				Part{ID: "2", MediaType: "video/mp4", Kind: "video"},
			),
			wantBodies:      []string{"0"},
			wantAttachments: []string{"1", "2"},
		},
		{
			name: "empty multiparts",
			root: multipart("multipart/mixed", "", multipart(mediaAlternative, "0"), multipart(mediaRelated, "1")),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shown := Choose(tt.root)

			assert.Equal(t, tt.wantBodies, ids(shown.Bodies), "bodies")
			assert.Equal(t, tt.wantAttachments, ids(shown.Attachments), "attachments")
			assert.Equal(t, tt.wantInline, ids(shown.Inline), "inline")
		})
	}
}

// ids returns the IDs of parts, or nil when there are none.
func ids(parts []Part) []string {
	var out []string
	for _, part := range parts {
		out = append(out, part.ID)
	}
	return out
}

// Nested alternatives with nothing to show take time in proportion to
// their parts, not twice as long per level.
func TestChooseDeepAlternatives(t *testing.T) {
	root := filePart("leaf")
	for range 200 {
		root = multipart(mediaAlternative, "", root)
	}

	shown := Choose(root)

	assert.Equal(t, []string{"leaf"}, ids(shown.Attachments))
}

// Inline parts no HTML body showed are offered after the attachments.
func TestDownloads(t *testing.T) {
	shown := Choose(multipart("multipart/mixed", "",
		multipart(mediaRelated, "0", htmlPart("0.0"), imagePart("0.1"), imagePart("0.2"), imagePart("0.3")),
		filePart("1"),
	))

	downloads := shown.Downloads([]string{"0.2"})

	assert.Equal(t, []string{"1", "0.1", "0.3"}, ids(downloads))
	assert.Equal(t, []string{"1"}, ids(shown.Attachments), "Downloads changed the attachments")
}
