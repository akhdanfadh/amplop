package google

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/textproto"
	"strings"

	"google.golang.org/api/gmail/v1"

	"inbok/internal/messagecontent"
)

// errNoPart means messages.get answered without a part where one belongs,
// e.g. a message without its payload.
var errNoPart = errors.New("gmail message: no part")

// receivedPart returns part, a message's payload or one of its parts from
// messages.get in the full format, with its children, as the
// [messagecontent.Received] that [messagecontent.Parse] reads. It returns
// an error when a part is missing or its data is not base64url.
func receivedPart(part *gmail.MessagePart) (messagecontent.Received, error) {
	if part == nil {
		return messagecontent.Received{}, errNoPart
	}

	header := make(textproto.MIMEHeader, len(part.Headers))
	for _, field := range part.Headers {
		header.Add(field.Name, field.Value)
	}
	received := messagecontent.Received{
		ID:        part.PartId,
		MediaType: part.MimeType,
		Filename:  part.Filename,
		Header:    header,
		Content:   []byte{},
	}
	if part.Body != nil {
		content, err := bodyContent(part.Body)
		if err != nil {
			return messagecontent.Received{}, fmt.Errorf("part %q: %w", part.PartId, err)
		}
		received.Size = part.Body.Size
		received.Content = content
		received.ContentCharset = dataCharset(part)
	}

	for _, child := range part.Parts {
		c, err := receivedPart(child)
		if err != nil {
			return messagecontent.Received{}, err
		}
		received.Parts = append(received.Parts, c)
	}
	return received, nil
}

// bodyContent returns the data of body, from messages.get, decoded: empty
// when it has none, or nil when Gmail sends it through
// messages.attachments.get instead. An attachment's bytes may come as
// data too.
func bodyContent(body *gmail.MessagePartBody) ([]byte, error) {
	if body.AttachmentId != "" {
		return nil, nil
	}
	// Gmail's base64url may or may not end in padding.
	content, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(body.Data, "="))
	if err != nil {
		return nil, fmt.Errorf("decode data: %w", err)
	}
	return content, nil
}

// dataUTF8 is the charset Gmail converts a text part's inline data to.
const dataUTF8 = "utf-8"

// dataCharset returns the charset of part's inline data when Gmail
// converted it, as [messagecontent.Received.ContentCharset]: UTF-8 for a
// text part, else empty.
//
// NOTE: Gmail converts a text/* part's data to UTF-8, from its declared
// charset, its <meta> charset, or else Latin-1, yet keeps the part's
// Content-Type as sent, and its size the length as sent. Bytes from
// messages.attachments.get, e.g. a long body's or a file's, come as sent
// (observed on real Gmail, 2026-11-12; ADR009, "What Gmail sends").
func dataCharset(part *gmail.MessagePart) string {
	if part.Body == nil || part.Body.AttachmentId != "" || !strings.HasPrefix(strings.ToLower(part.MimeType), "text/") {
		return ""
	}
	return dataUTF8
}
