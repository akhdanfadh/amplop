package messagecontent

import (
	"errors"
	"mime"
	"net/textproto"
	"strings"
	"unicode"
)

// Kind says what a [Part] is to a reader of the message.
type Kind string

const (
	// KindMultipart holds other parts, e.g. multipart/alternative.
	KindMultipart Kind = "multipart"
	// KindText is a text/plain or text/html part that is the message's
	// text, not a file: not marked as an attachment and without a filename.
	KindText Kind = "text"
	// KindAttachment is a file the member downloads.
	KindAttachment Kind = "attachment"
	// KindInline is an image HTML shows by its Content-ID, e.g. inside
	// multipart/related.
	KindInline Kind = "inline"
)

// Disposition is how the sender asked for a part to be shown
// (Content-Disposition, RFC 2183).
type Disposition string

const (
	DispositionNone       Disposition = ""
	DispositionInline     Disposition = "inline"
	DispositionAttachment Disposition = "attachment"
)

// Media types the parser and [Choose] tell apart.
const (
	mediaPlain       = "text/plain"
	mediaHTML        = "text/html"
	mediaAlternative = "multipart/alternative"
	mediaRelated     = "multipart/related"
	mediaMultipart   = "multipart/"
	mediaImage       = "image/"
)

// Header fields and parameters the parser reads.
const (
	headerContentType = "Content-Type"
	headerDisposition = "Content-Disposition"
	headerContentID   = "Content-ID"
	paramCharset      = "charset"
	paramStart        = "start"
)

// maxParts is the most parts [Parse] reads in one message. inbok's own
// limit: each part becomes a row, and real mail has tens at most.
const maxParts = 1000

// ErrTooManyParts means a message has more parts than inbok reads.
var ErrTooManyParts = errors.New("messagecontent: too many parts")

// Received is one part of a message as a provider returns it, with its
// children: the input to [Parse]. A provider's adapter fills it from its
// own types.
//
// DESIGN: Go's MIME libraries, e.g. emersion's go-message and enmime,
// read raw message bytes, while Gmail's full format arrives already
// parsed, so the Gmail adapter fills this directly. An adapter for raw
// mail, e.g. IMAP, would fill it with one of them. Either way, kinds and
// what to show are inbok's own rules, which neither library decides
// (ADR009, "Message parts").
type Received struct {
	ID        string               // the provider's part ID, e.g. Gmail's partId "0.1"
	MediaType string               // e.g. "text/html", without parameters
	Filename  string               // the suggested file name, as the provider decodes it
	Header    textproto.MIMEHeader // the part's own header fields
	Size      int64                // the content's length, transfer encoding undone
	// Content is the part's bytes, transfer encoding undone, or nil when
	// the provider sends them in a separate request, e.g. Gmail's
	// attachmentId. An empty part has empty, not nil, content.
	Content []byte
	Parts   []Received
}

// Part describes one part of a message: what PostgreSQL keeps of it. Its
// Parts give its place in the tree.
type Part struct {
	ID          string // the provider's part ID
	MediaType   string // lower case, e.g. "text/html"
	Charset     string // as the sender declared it, lower case; "" when none
	Filename    string
	Size        int64 // the content's length as received, before [DecodeText]
	Disposition Disposition
	ContentID   string // without angle brackets; "" when none
	Kind        Kind
	Parts       []Part
	// Start is, for multipart/related, the Content-ID of the part shown
	// (RFC 2387, section 3.2), without angle brackets; "" when none.
	Start string
}

// Text is a text part's content, converted to UTF-8.
type Text struct {
	PartID  string // the [Part.ID] it belongs to
	Content []byte
}

// Parse returns the part tree of a message whose top part is root, and
// the content of each text part that root carries, converted to UTF-8 by
// [DecodeText]. A text part whose content the provider sends separately
// has no [Text]; decode it with [DecodeText] once fetched. Neither does
// a text part inside an attachment, e.g. a forwarded message, which no
// view shows. Strings from the sender come back as valid UTF-8 without
// control or bidirectional formatting characters. It returns
// [ErrTooManyParts] when root's tree has more than 1000 parts.
func Parse(root Received) (Part, []Text, error) {
	if count(root) > maxParts {
		return Part{}, nil, ErrTooManyParts
	}
	var texts []Text
	tree := parse(root, &texts)
	return tree, texts, nil
}

// count returns the number of parts in received's tree.
func count(received Received) int {
	n := 1
	for _, child := range received.Parts {
		n += count(child)
	}
	return n
}

// parse returns received's part tree. When texts is not nil, it appends
// the content of received's text parts to it.
func parse(received Received, texts *[]Text) Part {
	part := Part{
		ID:          clean(received.ID),
		MediaType:   mediaType(received),
		Charset:     charsetOf(received.Header),
		Filename:    clean(received.Filename),
		Size:        received.Size,
		Disposition: dispositionOf(received.Header),
		ContentID:   contentID(received.Header),
		Start:       startOf(received.Header),
	}
	part.Kind = kindOf(part)

	if texts != nil && part.Kind == KindText && received.Content != nil {
		*texts = append(*texts, Text{PartID: part.ID, Content: DecodeText(part, received.Content)})
	}
	// Only a multipart's children can be shown; an attachment's are not.
	if part.Kind != KindMultipart {
		texts = nil
	}
	for _, child := range received.Parts {
		part.Parts = append(part.Parts, parse(child, texts))
	}
	return part
}

// mediaType returns received's media type in lower case: the provider's,
// else its Content-Type field's, else text/plain.
func mediaType(received Received) string {
	value := received.MediaType
	if strings.TrimSpace(value) == "" {
		value = received.Header.Get(headerContentType)
	}
	// ParseMediaType returns the type even when a parameter is malformed.
	t, _, _ := mime.ParseMediaType(value)
	// A part without a valid Content-Type is plain text (RFC 2045, section 5.2).
	if t == "" {
		return mediaPlain
	}
	return t
}

// charsetOf returns the charset parameter of header's Content-Type in
// lower case, or "" when it has none.
func charsetOf(header textproto.MIMEHeader) string {
	value := header.Get(headerContentType)
	_, params, err := mime.ParseMediaType(value)
	if err == nil {
		return strings.ToLower(clean(params[paramCharset]))
	}
	// ParseMediaType drops every parameter when one is malformed, e.g. an
	// unquoted file name with a space, so look for the charset alone.
	_, rest, _ := strings.Cut(value, ";")
	for param := range strings.SplitSeq(rest, ";") {
		name, label, ok := strings.Cut(param, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), paramCharset) {
			continue
		}
		return strings.ToLower(clean(paramValue(label)))
	}
	return ""
}

// paramValue returns the value at the start of s, a parameter's text
// after "=": a quoted string without its quotes, else a token, which ends
// at white space (RFC 2045, section 5.1).
func paramValue(s string) string {
	s = strings.TrimSpace(s)
	if quoted, ok := strings.CutPrefix(s, `"`); ok {
		value, _, _ := strings.Cut(quoted, `"`)
		return value
	}
	if end := strings.IndexAny(s, " \t"); end >= 0 {
		return s[:end]
	}
	return s
}

// dispositionOf returns header's Content-Disposition type.
func dispositionOf(header textproto.MIMEHeader) Disposition {
	value := header.Get(headerDisposition)
	if strings.TrimSpace(value) == "" {
		return DispositionNone
	}
	// ParseMediaType returns the type even when it cannot read the parameters.
	t, _, _ := mime.ParseMediaType(value)
	if t == string(DispositionInline) {
		return DispositionInline
	}
	// An unknown type is an attachment (RFC 2183, section 2.8).
	return DispositionAttachment
}

// contentID returns header's Content-ID, or "" when it has none.
func contentID(header textproto.MIMEHeader) string {
	return unbracket(header.Get(headerContentID))
}

// startOf returns the start parameter of header's Content-Type, or ""
// when it has none or cannot be read.
func startOf(header textproto.MIMEHeader) string {
	_, params, err := mime.ParseMediaType(header.Get(headerContentType))
	if err != nil {
		return ""
	}
	return unbracket(params[paramStart])
}

// unbracket returns id, a Content-ID, without its angle brackets
// (RFC 2392).
func unbracket(id string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "<") && strings.HasSuffix(id, ">") {
		id = id[1 : len(id)-1]
	}
	return clean(strings.TrimSpace(id))
}

// kindOf returns part's kind from its media type, disposition, filename,
// and Content-ID, following the terms of ADR009.
func kindOf(part Part) Kind {
	text := part.MediaType == mediaPlain || part.MediaType == mediaHTML
	switch {
	case strings.HasPrefix(part.MediaType, mediaMultipart):
		return KindMultipart
	case text && part.Disposition != DispositionAttachment && part.Filename == "":
		return KindText
	// Only images are shown by Content-ID; any other part, e.g. HTML or a
	// program, is offered as a file instead.
	case strings.HasPrefix(part.MediaType, mediaImage) && part.ContentID != "" && part.Disposition != DispositionAttachment:
		return KindInline
	default:
		return KindAttachment
	}
}

// clean returns s, a sender's string, as valid UTF-8 without control
// characters, which PostgreSQL's text refuses (NUL) or a log or file name
// misreads (CR, LF), and without bidirectional formatting characters,
// which can disguise a file name, e.g. U+202E turning "gpj.exe" around.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "\uFFFD"))
}
