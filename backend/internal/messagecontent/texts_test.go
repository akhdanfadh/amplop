package messagecontent_test

import (
	"context"
	"io"
	"net/textproto"
	"strconv"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/messagecontent"
)

// textsOf stores message as account [fetchAccount]'s message "m1", and
// returns a reader of its texts, its stack, and its ID.
func textsOf(t *testing.T, message messagecontent.Received) (*messagecontent.Texts, fetchStack, uuid.UUID) {
	t.Helper()
	storage, ids := newFetchMemory("m1")
	provider := &fakeProvider{messages: map[string]messagecontent.Received{"m1": message}}
	s := newFetchStack(storage, provider, fakeAccess{})
	require.NoError(t, s.fetcher.FetchMessage(t.Context(), fetchAccount, ids[0]))
	return messagecontent.NewTexts(s.store, storage), s, ids[0]
}

// roomy is limits past any test's text.
var roomy = messagecontent.TextLimits{Parts: 64, Bytes: 1 << 20}

// readOf returns texts' texts of [fetchAccount]'s message id within limits.
func readOf(t *testing.T, texts *messagecontent.Texts, id uuid.UUID, limits messagecontent.TextLimits) messagecontent.MessageTexts {
	t.Helper()
	read, err := texts.Read(t.Context(), fetchAccount, id, limits)
	require.NoError(t, err)
	return read
}

// part returns a text part with id, mediaType, and content.
func part(id, mediaType, content string) messagecontent.Received {
	return messagecontent.Received{ID: id, MediaType: mediaType, Header: textproto.MIMEHeader{}, Content: []byte(content)}
}

// multipart returns a container of mediaType holding parts.
func multipart(mediaType string, parts ...messagecontent.Received) messagecontent.Received {
	return messagecontent.Received{MediaType: mediaType, Header: textproto.MIMEHeader{}, Parts: parts}
}

// plain and html return a stored text of their media type.
func plain(content string) messagecontent.StoredText {
	return messagecontent.StoredText{MediaType: "text/plain", Content: []byte(content)}
}

func html(content string) messagecontent.StoredText {
	return messagecontent.StoredText{MediaType: "text/html", Content: []byte(content)}
}

// Every text part is read, as shown when a view shows it and as hidden
// otherwise, whichever alternative carries the words.
func TestTextsRead(t *testing.T) {
	image := messagecontent.Received{ID: "img", MediaType: "image/png", Header: textproto.MIMEHeader{"Content-Id": {"<banner>"}}}
	cases := map[string]struct {
		message messagecontent.Received
		want    messagecontent.MessageTexts
	}{
		"a stub plain alternative is hidden": {
			multipart("multipart/alternative", part("0", "text/plain", "View in browser"), part("1", "text/html", "<p>Sale</p>")),
			messagecontent.MessageTexts{Shown: []messagecontent.StoredText{html("<p>Sale</p>")}, Hidden: []messagecontent.StoredText{plain("View in browser")}},
		},
		"the words of an image-only HTML's plain alternative": {
			multipart("multipart/alternative", part("0", "text/plain", "Running shoes"), part("1", "text/html", `<img src="cid:banner">`)),
			messagecontent.MessageTexts{Shown: []messagecontent.StoredText{html(`<img src="cid:banner">`)}, Hidden: []messagecontent.StoredText{plain("Running shoes")}},
		},
		"the last alternative with HTML is shown whole": {
			multipart("multipart/alternative",
				part("0", "text/plain", "one"),
				multipart("multipart/mixed", part("1.0", "text/plain", "two"), part("1.1", "text/html", "<p>three</p>"))),
			messagecontent.MessageTexts{Shown: []messagecontent.StoredText{plain("two"), html("<p>three</p>")}, Hidden: []messagecontent.StoredText{plain("one")}},
		},
		"of two HTML alternatives, the last is shown": {
			multipart("multipart/alternative", part("0", "text/html", "<p>old</p>"), part("1", "text/html", "<p>new</p>")),
			messagecontent.MessageTexts{Shown: []messagecontent.StoredText{html("<p>new</p>")}, Hidden: []messagecontent.StoredText{html("<p>old</p>")}},
		},
		"a footer in mixed is shown after the body": {
			multipart("multipart/mixed",
				multipart("multipart/alternative", part("0.0", "text/plain", "body"), part("0.1", "text/html", "<p>body</p>")),
				part("1", "text/plain", "footer")),
			messagecontent.MessageTexts{Shown: []messagecontent.StoredText{html("<p>body</p>"), plain("footer")}, Hidden: []messagecontent.StoredText{plain("body")}},
		},
		"a stray text part of related is hidden": {
			multipart("multipart/related", part("0", "text/html", "<p>main</p>"), part("1", "text/plain", "stray"), image),
			// Offered as a download, so the message has an attachment.
			messagecontent.MessageTexts{Shown: []messagecontent.StoredText{html("<p>main</p>")}, Hidden: []messagecontent.StoredText{plain("stray")}, HasAttachment: true},
		},
		"a lone plain part": {
			part("", "text/plain", "hello"),
			messagecontent.MessageTexts{Shown: []messagecontent.StoredText{plain("hello")}},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			texts, _, id := textsOf(t, c.message)

			assert.Equal(t, c.want, readOf(t, texts, id, roomy))
		})
	}
}

// The limits are shared, and the shown parts read first: the part a
// budget runs out in is cut, and the hidden ones get what is left.
func TestTextsBudget(t *testing.T) {
	message := multipart("multipart/mixed",
		multipart("multipart/alternative", part("0.0", "text/plain", "hidden"), part("0.1", "text/html", "aaaa")),
		part("1", "text/plain", "bbbb"))
	texts, _, id := textsOf(t, message)

	bytes := readOf(t, texts, id, messagecontent.TextLimits{Parts: 64, Bytes: 6})
	parts := readOf(t, texts, id, messagecontent.TextLimits{Parts: 2, Bytes: 1 << 20})

	assert.Equal(t, messagecontent.MessageTexts{Shown: []messagecontent.StoredText{html("aaaa"), plain("bb")}}, bytes)
	assert.Equal(t, messagecontent.MessageTexts{Shown: []messagecontent.StoredText{html("aaaa"), plain("bbbb")}}, parts)
}

// countingStore counts the contents it is asked for.
type countingStore struct {
	messagecontent.Store
	gets atomic.Int32
}

func (s *countingStore) Get(ctx context.Context, ref messagecontent.Ref) (io.ReadCloser, error) {
	s.gets.Add(1)
	return s.Store.Get(ctx, ref)
}

// At most limits.Parts parts are opened, however small they are.
func TestTextsParts(t *testing.T) {
	parts := make([]messagecontent.Received, 5)
	for i := range parts {
		parts[i] = part(strconv.Itoa(i), "text/plain", "")
	}
	_, s, id := textsOf(t, multipart("multipart/mixed", parts...))
	store := &countingStore{Store: s.store}
	texts := messagecontent.NewTexts(store, s.storage)

	read := readOf(t, texts, id, messagecontent.TextLimits{Parts: 3, Bytes: 1 << 20})

	assert.Len(t, read.Shown, 3)
	assert.Equal(t, int32(3), store.gets.Load())
}

// A message with no parts stored has no texts, and one whose content the
// store lost has none either, rather than an error.
func TestTextsNone(t *testing.T) {
	storage, ids := newFetchMemory("m1")
	store := newFetchStack(storage, &fakeProvider{}, fakeAccess{}).store
	never := readOf(t, messagecontent.NewTexts(store, storage), ids[0], roomy)

	lost, s, id := textsOf(t, alternative("Hello"))
	require.NoError(t, s.store.DeleteAccount(t.Context(), fetchAccount))

	assert.Equal(t, messagecontent.MessageTexts{}, never)
	assert.Equal(t, messagecontent.MessageTexts{}, readOf(t, lost, id, roomy))
}

// file returns a part of mediaType sent as a file named name, whose bytes
// the provider sends apart, as Gmail does by attachmentId.
func file(id, mediaType, name string, header textproto.MIMEHeader) messagecontent.Received {
	if header == nil {
		header = textproto.MIMEHeader{}
	}
	return messagecontent.Received{ID: id, MediaType: mediaType, Filename: name, Header: header, Size: 100}
}

// A message has an attachment when a view offers one of its parts as a
// download, read from the whole tree: a photo sent beside the text counts,
// a logo its HTML shows does not, nor a file in an alternative a view does
// not pick, and the text limits change nothing.
func TestTextsAttachment(t *testing.T) {
	pdf := file("1", "application/pdf", "invoice.pdf", textproto.MIMEHeader{"Content-Disposition": {`attachment; filename="invoice.pdf"`}})
	cases := map[string]struct {
		message messagecontent.Received
		want    bool
	}{
		"text only": {alternative("Hello"), false},
		"a PDF beside the text": {
			multipart("multipart/mixed", part("0", "text/plain", "See attached"), pdf),
			true,
		},
		"nothing but a file": {file("", "application/pdf", "scan.pdf", nil), true},
		// As a phone sends a photo: an image with a Content-ID, marked
		// inline, beside the text rather than inside multipart/related.
		"a photo beside the text": {
			multipart("multipart/mixed",
				part("0", "text/plain", "Look"),
				file("1", "image/jpeg", "IMG_0001.jpeg", textproto.MIMEHeader{"Content-Id": {"<photo1>"}, "Content-Disposition": {`inline; filename="IMG_0001.jpeg"`}})),
			true,
		},
		"a logo the HTML shows": {
			multipart("multipart/related",
				part("0", "text/html", `<p>Hi</p><img src="cid:logo">`),
				file("1", "image/png", "logo.png", textproto.MIMEHeader{"Content-Id": {"<logo>"}})),
			false,
		},
		"a file in an alternative not shown": {
			multipart("multipart/alternative",
				multipart("multipart/mixed", part("0.0", "text/plain", "Plain"), file("0.1", "application/pdf", "old.pdf", nil)),
				part("1", "text/html", "<p>Rich</p>")),
			false,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			texts, _, id := textsOf(t, c.message)

			roomyRead := readOf(t, texts, id, roomy)
			none := readOf(t, texts, id, messagecontent.TextLimits{})

			assert.Equal(t, c.want, roomyRead.HasAttachment)
			assert.Equal(t, c.want, none.HasAttachment, "with no text read")
		})
	}
}
