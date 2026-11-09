package messagecontent

import (
	"bytes"
	"strings"
	"unicode/utf8"

	cssinput "github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
)

// words returns a set of lower-case CSS names.
func words(list ...string) map[string]bool {
	names := make(map[string]bool, len(list))
	for _, name := range list {
		names[name] = true
	}
	return names
}

// What CSS a sanitized document keeps: the properties and media queries
// Gmail supports, so CSS written for the client most mail is tested in
// keeps working (https://developers.google.com/workspace/gmail/design/css).
// Gmail's list leaves out position, which lets a message cover the page
// around it, and documents no @import or @font-face. The properties it
// keeps that can load an image, e.g. background, pass through
// [sanitizer.value]'s own rules for url().
var (
	properties = words("azimuth", "background", "background-blend-mode",
		"background-clip", "background-color", "background-image",
		"background-origin", "background-position", "background-repeat",
		"background-size", "border", "border-bottom", "border-bottom-color",
		"border-bottom-left-radius", "border-bottom-right-radius",
		"border-bottom-style", "border-bottom-width", "border-collapse",
		"border-color", "border-left", "border-left-color", "border-left-style",
		"border-left-width", "border-radius", "border-right",
		"border-right-color", "border-right-style", "border-right-width",
		"border-spacing", "border-style", "border-top", "border-top-color",
		"border-top-left-radius", "border-top-right-radius",
		"border-top-style", "border-top-width", "border-width", "box-sizing",
		"break-after", "break-before", "break-inside", "caption-side", "clear",
		"color", "column-count", "column-fill", "column-gap", "column-rule",
		"column-rule-color", "column-rule-style", "column-rule-width",
		"column-span", "column-width", "columns", "direction", "display",
		"elevation", "empty-cells", "float", "font", "font-family",
		"font-feature-settings", "font-kerning", "font-size",
		"font-size-adjust", "font-stretch", "font-style", "font-synthesis",
		"font-variant", "font-variant-alternates", "font-variant-caps",
		"font-variant-east-asian", "font-variant-ligatures",
		"font-variant-numeric", "font-weight", "height", "image-orientation",
		"image-resolution", "ime-mode", "isolation", "layout-flow",
		"layout-grid", "layout-grid-char", "layout-grid-char-spacing",
		"layout-grid-line", "layout-grid-mode", "layout-grid-type",
		"letter-spacing", "line-break", "line-height", "list-style",
		"list-style-position", "list-style-type", "margin", "margin-bottom",
		"margin-left", "margin-right", "margin-top", "marker-offset",
		"max-height", "max-width", "min-height", "min-width", "mix-blend-mode",
		"object-fit", "object-position", "opacity", "outline", "outline-color",
		"outline-style", "outline-width", "overflow", "overflow-x",
		"overflow-y", "padding", "padding-bottom", "padding-left",
		"padding-right", "padding-top", "page-break-after",
		"page-break-before", "page-break-inside", "pause", "pause-after",
		"pause-before", "pitch", "pitch-range", "quotes", "richness", "speak",
		"speak-header", "speak-numeral", "speak-punctuation", "speech-rate",
		"stress", "table-layout", "text-align", "text-align-last",
		"text-autospace", "text-combine-upright", "text-decoration",
		"text-decoration-color", "text-decoration-line",
		"text-decoration-skip", "text-decoration-style", "text-emphasis",
		"text-emphasis-color", "text-emphasis-style", "text-indent",
		"text-justify", "text-kashida-space", "text-orientation",
		"text-overflow", "text-transform", "text-underline-position",
		"unicode-bidi", "vertical-align", "voice-family", "white-space",
		"width", "word-break", "word-spacing", "word-wrap", "writing-mode",
		"zoom")

	mediaTypes    = words("all", "screen")
	mediaKeywords = words("and", "only")
	mediaFeatures = words("min-width", "max-width", "min-device-width",
		"max-device-width", "orientation", "min-resolution", "max-resolution")
	// orientation's values (Media Queries Level 4, 4.4).
	mediaValues = words("portrait", "landscape")
)

// functions are the CSS functions a value may call, with their opening
// parenthesis as the lexer gives them: colors, arithmetic, and
// gradients, which load nothing. Any other, e.g. var(), whose fallback
// can hide a URL, or image-set(), is refused.
var functions = words("rgb(", "rgba(", "hsl(", "hsla(", "calc(", "min(",
	"max(", "clamp(", "linear-gradient(", "radial-gradient(",
	"repeating-linear-gradient(", "repeating-radial-gradient(")

// delims are the single characters a value may hold outside other
// tokens: !important, the slash of font: 12px/1.5, and calc()'s
// operators and decimal point.
var delims = words("!", "/", "+", "-", "*", ".")

// selectorDelims are a class's dot, the universal selector, and the
// combinators, the single characters a selector may hold.
var selectorDelims = words(".", "*", ">", "+", "~")

// cssNone stands for a url() that [Sanitize] removes, so the rest of the
// declaration, e.g. a background color, still applies.
const cssNone = "none"

// stylesheet returns the rules of src, a style element's content, that
// [Sanitize] keeps, written again: rulesets whose selectors use elements,
// classes, IDs, and combinators only, inside at most one @media rule
// with a query Gmail supports. It skips what the parser cannot read, as
// a browser does, and closes the rules it opened at the end.
func (s *sanitizer) stylesheet(src []byte) []byte {
	// NOTE: NewInputBytes writes a NUL after src, into its spare
	// capacity, and src is the HTML tokenizer's buffer, which holds the
	// style element's end tag there; a copy keeps it.
	parser := css.NewParser(cssinput.NewInputBytes(bytes.Clone(src)), false)
	var out bytes.Buffer
	// skip counts the blocks of a dropped rule still open.
	skip := 0
	media, rule := false, false
	skipped := progress{}
	for {
		grammar, _, data := parser.Next()
		switch grammar {
		// A parse error is skipped; the end of the input closes the rules
		// still open, which the parser does not always end itself, e.g.
		// after "@media{)".
		case css.ErrorGrammar:
			if skipped.recover(parser) {
				continue
			}
			if rule {
				out.WriteByte('}')
			}
			if media {
				out.WriteByte('}')
			}
			return out.Bytes()
		// An at-rule with a block: @media, kept when it is not nested and
		// its query passes; any other, e.g. @font-face, goes with its block.
		case css.BeginAtRuleGrammar:
			if skip > 0 || media || rule || !bytes.EqualFold(data, []byte("@media")) {
				skip++
				continue
			}
			query, ok := mediaQuery(parser.Values())
			if !ok {
				skip++
				continue
			}
			media = true
			out.WriteString("@media ")
			out.Write(query)
			out.WriteByte('{')
		// A ruleset, kept when it is not nested and its selector passes.
		case css.BeginRulesetGrammar:
			selector, ok := selector(parser.Values())
			if skip > 0 || rule || !ok {
				skip++
				continue
			}
			rule = true
			out.Write(selector)
			out.WriteByte('{')
		// The end of a block: the innermost one open, dropped or kept.
		case css.EndAtRuleGrammar, css.EndRulesetGrammar:
			switch {
			case skip > 0:
				skip--
			case rule:
				rule = false
				out.WriteByte('}')
			case media:
				media = false
				out.WriteByte('}')
			}
		// A declaration, kept only inside a kept ruleset.
		case css.DeclarationGrammar:
			if skip == 0 && rule {
				s.declaration(&out, data, parser.Values())
			}
		// At-rules without a block, e.g. @import, custom properties, and
		// comments.
		default:
		}
	}
}

// declarations returns the declarations of src, a style attribute's
// value, that [Sanitize] keeps, written again.
func (s *sanitizer) declarations(src string) []byte {
	parser := css.NewParser(cssinput.NewInputString(src), true)
	var out bytes.Buffer
	skipped := progress{}
	for {
		grammar, _, data := parser.Next()
		switch grammar {
		// A parse error is skipped; the end of the input ends the list.
		case css.ErrorGrammar:
			if skipped.recover(parser) {
				continue
			}
			return out.Bytes()
		case css.DeclarationGrammar:
			s.declaration(&out, data, parser.Values())
		// Custom properties and comments.
		default:
		}
	}
}

// lower returns name, a CSS name, in lower case, or "" when it holds a
// character outside ASCII, which matches no name in the lists. CSS names
// are ASCII case-insensitive (CSS Syntax Level 3, 2.1); bytes.ToLower
// would also fold e.g. U+0130 into "i", passing a name a browser does not
// know.
func lower(name []byte) []byte {
	out := make([]byte, len(name))
	for i, c := range name {
		switch {
		case c >= utf8.RuneSelf:
			return nil
		case 'A' <= c && c <= 'Z':
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return out
}

// progress tells a parse error the parser skips from the end of its
// input.
type progress struct {
	seen   bool
	offset int // where the last parse error was
}

// recover reports whether parser, which has just returned an error, can
// read on: the error is in the CSS, not the end of the input, and the
// parser has moved on since the last one.
//
// DESIGN: v2.8.20 ends every input with an error that is not a parse
// error, so the offset check never decides today; it keeps a parser that
// reported the same parse error forever from hanging a request.
func (p *progress) recover(parser *css.Parser) bool {
	offset := parser.Offset()
	if !parser.HasParseError() || (p.seen && offset == p.offset) {
		return false
	}
	p.seen, p.offset = true, offset
	return true
}

// declaration writes to out the declaration of property with values, if
// [properties] holds it and its value passes [sanitizer.value].
func (s *sanitizer) declaration(out *bytes.Buffer, property []byte, values []css.Token) {
	name := lower(property)
	if !properties[string(name)] {
		return
	}
	value, ok := s.value(values)
	if !ok {
		return
	}
	out.Write(name)
	out.WriteByte(':')
	out.Write(value)
	out.WriteByte(';')
}

// value returns values written again, or false when one of them is not
// allowed: a CSS escape, which can hide a name or a URL from a filter
// (PortSwigger, "CSS: the bomb inside your inbox", 2026); a <, which
// could end the style element; a function outside [functions]; or an
// unterminated string or unbalanced parentheses. Each url() becomes
// [sanitizer.image]'s URL, or [cssNone]; their inline parts and blocked
// remote images are noted only when the value is kept.
func (s *sanitizer) value(values []css.Token) ([]byte, bool) {
	var out []byte
	var images []imageURL
	depth := 0
	for _, token := range values {
		data := token.Data
		if bytes.ContainsAny(data, `\<`) {
			return nil, false
		}
		switch token.TokenType {
		// Whitespace: [join] separates every token anyway.
		case css.WhitespaceToken:
			continue
		// Keywords, numbers, lengths, colors, and commas, copied.
		case css.IdentToken, css.NumberToken, css.PercentageToken,
			css.DimensionToken, css.HashToken, css.CommaToken:
		// A quoted string, e.g. a font name.
		case css.StringToken:
			// A string the content ends inside would take in what follows.
			if len(data) < 2 || data[len(data)-1] != data[0] {
				return nil, false
			}
		// A single character, only one of [delims].
		case css.DelimToken:
			if !delims[string(data)] {
				return nil, false
			}
		// A function, only one of [functions], open until its ).
		case css.FunctionToken:
			if !functions[string(lower(data))] {
				return nil, false
			}
			depth++
		// A ( inside a function, e.g. calc((1px + 2px) * 2).
		case css.LeftParenthesisToken:
			depth++
		// The ) of an open function or (; any other is refused.
		case css.RightParenthesisToken:
			if depth == 0 {
				return nil, false
			}
			depth--
		// url(): the image's URL rewritten, or none.
		case css.URLToken:
			var img imageURL
			data, img = s.cssURL(data)
			images = append(images, img)
		// Anything else, e.g. a brace, a semicolon, an @keyword, a bad
		// string or URL, or a comment, refuses the value.
		default:
			return nil, false
		}
		out = join(out, data)
	}
	if depth != 0 {
		return nil, false
	}
	for _, img := range images {
		s.note(img)
	}
	return out, true
}

// cssURL returns what to write for data, a url() token without
// backslashes: url() with the URL [sanitizer.image] returns, or
// [cssNone], and the image it describes.
func (s *sanitizer) cssURL(data []byte) ([]byte, imageURL) {
	const open, end = "url(", ")"
	if len(data) < len(open)+len(end) || !bytes.EqualFold(data[:len(open)], []byte(open)) || !bytes.HasSuffix(data, []byte(end)) {
		return []byte(cssNone), imageURL{}
	}
	inner := bytes.TrimSpace(data[len(open) : len(data)-len(end)])
	if len(inner) >= 2 && (inner[0] == '"' || inner[0] == '\'') && inner[len(inner)-1] == inner[0] {
		inner = inner[1 : len(inner)-1]
	}
	img := s.image(string(inner))
	// The URL is written as a quoted string, which only these characters
	// could end or escape (CSS Syntax Level 3, 4.3.5); url.URL.String
	// leaves a quote in a query as it is.
	if img.url == "" || strings.ContainsAny(img.url, "\"\\<\n\r\f") {
		return []byte(cssNone), imageURL{blocked: img.blocked}
	}
	return []byte(open + `"` + img.url + `"` + end), img
}

// selector returns tokens, a ruleset's selector, written again, or false
// unless it selects by element, class, and ID only, with combinators and
// commas, the selectors Gmail supports.
func selector(tokens []css.Token) ([]byte, bool) {
	var out []byte
	// prev is the type of the token written last, and space whether
	// whitespace came after it.
	var prev css.TokenType
	space := false
	for _, token := range tokens {
		data := token.Data
		if bytes.IndexByte(data, '\\') >= 0 {
			return nil, false
		}
		switch token.TokenType {
		// The descendant combinator, as one space before the next token.
		case css.WhitespaceToken:
			space = len(out) > 0
			continue
		// An element or class name, after which no space could only mean
		// a comment the parser dropped, e.g. p/**/q, whose two names
		// written together would read as one.
		case css.IdentToken:
			if !space && (prev == css.IdentToken || prev == css.HashToken) {
				return nil, false
			}
		// An #id, or a comma between selectors.
		case css.HashToken, css.CommaToken:
		// Only one of [selectorDelims].
		case css.DelimToken:
			if !selectorDelims[string(data)] {
				return nil, false
			}
		// Anything else, e.g. a pseudo-class's colon, an attribute
		// selector's [, or a function such as :not(, refuses the ruleset.
		default:
			return nil, false
		}
		if space {
			out = append(out, ' ')
		}
		out = append(out, data...)
		prev, space = token.TokenType, false
	}
	return out, len(out) > 0
}

// join returns out with data, a token, after it, and a space between
// them unless out is empty. The parser drops comments and some
// whitespace between tokens, so writing two of them together could make
// a browser read others: "/" and "*" a comment's start, "none" and
// "linear-gradient(" one function. Whitespace ends every CSS token
// (CSS Syntax Level 3, 4.3), and in a value or a media query it means
// nothing more.
func join(out, data []byte) []byte {
	if len(out) > 0 {
		out = append(out, ' ')
	}
	return append(out, data...)
}

// mediaQuery returns tokens, an @media rule's query, written again, or
// false unless it is a list of media types, and, only, and parenthesized
// features with a value, as [mediaTypes], [mediaKeywords], and
// [mediaFeatures] allow.
func mediaQuery(tokens []css.Token) ([]byte, bool) {
	var out []byte
	inFeature, afterColon := false, false
	for _, token := range tokens {
		data := token.Data
		if bytes.IndexByte(data, '\\') >= 0 {
			return nil, false
		}
		ok := true
		switch token.TokenType {
		// Whitespace: [join] separates every token anyway.
		case css.WhitespaceToken:
			continue
		// Between queries, outside a feature.
		case css.CommaToken:
			ok = !inFeature
		// Opens a feature, e.g. (max-width:600px); features do not nest.
		case css.LeftParenthesisToken:
			ok = !inFeature
			inFeature, afterColon = true, false
		// Closes a feature, which must have had its value.
		case css.RightParenthesisToken:
			ok = inFeature && afterColon
			inFeature = false
		// Between a feature's name and its value, once.
		case css.ColonToken:
			ok = inFeature && !afterColon
			afterColon = true
		case css.IdentToken:
			name := string(lower(data))
			switch {
			// A media type, e.g. screen, or and or only.
			case !inFeature:
				ok = mediaTypes[name] || mediaKeywords[name]
			// A feature's name, e.g. max-width.
			case !afterColon:
				ok = mediaFeatures[name]
			// orientation's value, e.g. portrait.
			default:
				ok = mediaValues[name]
			}
		// A feature's value, e.g. 600px or 2dppx.
		case css.NumberToken, css.DimensionToken:
			ok = afterColon && inFeature
		// Anything else, e.g. a function or a string, refuses the query.
		default:
			ok = false
		}
		if !ok {
			return nil, false
		}
		out = join(out, data)
	}
	return out, !inFeature
}
