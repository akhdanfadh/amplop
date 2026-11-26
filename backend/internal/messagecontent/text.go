package messagecontent

import (
	"bytes"
	"mime"

	"golang.org/x/net/html/charset"
)

// bomUTF8 is the UTF-8 byte order mark, which some senders put first.
var bomUTF8 = []byte{0xEF, 0xBB, 0xBF}

// DecodeText returns content, a text part's bytes as received, converted from
// part's charset to valid UTF-8. A byte it cannot convert becomes U+FFFD.
//
// DESIGN: It reads content as a browser reads a page, through
// [charset.DetermineEncoding] (WHATWG Encoding Standard): a byte order mark,
// then the declared charset, then a <meta> element in the first 1024 bytes,
// then UTF-8 if those bytes are valid UTF-8, else windows-1252. So us-ascii and
// iso-8859-1 read as windows-1252, which senders often mean, and a charset the
// standard calls unsafe, e.g. iso-2022-kr, reads as one U+FFFD. Like a browser,
// it garbles UTF-8 that is labelled windows-1252, or undeclared after a first
// kilobyte of plain ASCII.
func DecodeText(part Part, content []byte) []byte {
	// NOTE: Without a declared charset, DetermineEncoding drops a last
	// multi-byte character as if cut off, so "caf\xc3\xa9" alone reads as
	// windows-1252; text that ends in a line break, as mail does, does not.
	e, _, _ := charset.DetermineEncoding(content, contentType(part))
	decoded, err := e.NewDecoder().Bytes(content)
	if err != nil {
		decoded = content
	}
	// The UTF-8 decoder passes invalid bytes through as they are.
	return bytes.TrimPrefix(bytes.ToValidUTF8(decoded, []byte("\uFFFD")), bomUTF8)
}

// DecodeFrom returns content, a text part's bytes, converted to valid
// UTF-8 as [DecodeText] does, from contentCharset when the provider
// converted the bytes to it, or else from part's declared charset when
// contentCharset is empty.
func DecodeFrom(part Part, contentCharset string, content []byte) []byte {
	if contentCharset != "" {
		part.Charset = contentCharset
	}
	return DecodeText(part, content)
}

// contentType returns part's media type with its declared charset, as a
// Content-Type value.
func contentType(part Part) string {
	if part.Charset == "" {
		return part.MediaType
	}
	return mime.FormatMediaType(part.MediaType, map[string]string{paramCharset: part.Charset})
}
