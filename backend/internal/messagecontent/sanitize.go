package messagecontent

import (
	"bufio"
	"io"
	"iter"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Remote says whether a view of a message loads remote images: images
// from the sender's servers, which tell the sender that, when, and where
// the message was read.
type Remote int

const (
	// RemoteBlocked leaves remote images out, the default.
	RemoteBlocked Remote = iota + 1
	// RemoteAllowed loads them, for one view a member asked for.
	RemoteAllowed
)

// Policy is what [Sanitize] needs to know about the view it serves.
type Policy struct {
	// Inline is the inline parts the HTML may show by a cid: URL, as
	// [Display.Inline] lists them.
	Inline []Part
	// PartURL returns the URL that serves the part of this message with
	// the given ID. Without it, cid: URLs show nothing.
	PartURL func(partID string) string
	Remote  Remote
}

// Sanitized is what [Sanitize] found in a document.
type Sanitized struct {
	// Shown is the IDs of the inline parts the document shows, in the
	// order it first shows them.
	Shown []string
	// Blocked says whether the document refers to remote images that
	// [RemoteBlocked] left out, so a view can offer to load them.
	Blocked bool
}

// The URL schemes a sanitized document keeps.
const (
	schemeHTTP   = "http"
	schemeHTTPS  = "https"
	schemeMailto = "mailto"
	schemeTel    = "tel"
	schemeCID    = "cid:" // RFC 2392, with its colon
)

// Attributes [Sanitize] rewrites or reads rather than copies.
const (
	attrHref       = "href"
	attrSrc        = "src"
	attrBackground = "background"
	attrStyle      = "style"
	attrAria       = "aria-"
	attrType       = "type"
	attrMedia      = "media"
)

// mediaCSS is the one style sheet type a browser applies (HTML Standard
// 4.2.6).
const mediaCSS = "text/css"

// maxCSS is the most bytes of CSS, in style elements and style
// attributes together, that [Sanitize] reads in one document; a style
// sheet or attribute past what is left of it is dropped.
//
// DESIGN: the CSS parser holds every token of a declaration or a rule's
// prelude at once, so crafted CSS takes about a hundred times its size in
// memory: 10 MB took over 1 GB. 256 KiB caps that near 40 MB. Gmail clips
// a message whose HTML passes 102 KB, so mail meant to be read keeps its
// CSS well under it; past it, the text still shows, unstyled.
const maxCSS = 256 << 10

// doctype starts every sanitized document, so a browser lays it out in
// standards mode, as Gmail's web client, where senders test, shows mail.
const doctype = "<!DOCTYPE html>"

// linkTarget opens a link in a new tab, as its own page: a link in a
// sandboxed frame must never replace inbok's page, and the page it opens
// learns nothing of inbok (rel, HTML Standard 4.6.6).
const linkTarget = ` target="_blank" rel="noopener noreferrer"`

// set returns a set of elements, by the atom the tokenizer gives each.
func set(list ...atom.Atom) map[atom.Atom]bool {
	elements := make(map[atom.Atom]bool, len(list))
	for _, element := range list {
		elements[element] = true
	}
	return elements
}

var (
	// kept elements are copied with the attributes [attributes] allows.
	// They are the HTML Standard's sectioning, grouping, text-level, edit,
	// and table elements, details, and img, plus the obsolete
	// presentational elements mail still uses, e.g. center and font. Any
	// other element is unwrapped: its tags are dropped and its content
	// kept, e.g. form, button, and Outlook's <o:p>.
	kept = set(atom.Html, atom.Head, atom.Body,
		atom.Address, atom.Article, atom.Aside, atom.Footer, atom.Header,
		atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Hgroup,
		atom.Main, atom.Nav, atom.Section,
		atom.Blockquote, atom.Dd, atom.Div, atom.Dl, atom.Dt, atom.Figcaption,
		atom.Figure, atom.Hr, atom.Li, atom.Ol, atom.P, atom.Pre, atom.Ul,
		atom.A, atom.Abbr, atom.B, atom.Bdi, atom.Bdo, atom.Br, atom.Cite,
		atom.Code, atom.Data, atom.Dfn, atom.Em, atom.I, atom.Kbd, atom.Mark,
		atom.Q, atom.Rp, atom.Rt, atom.Ruby, atom.S, atom.Samp, atom.Small,
		atom.Span, atom.Strong, atom.Sub, atom.Sup, atom.Time, atom.U,
		atom.Var, atom.Wbr, atom.Del, atom.Ins,
		atom.Caption, atom.Col, atom.Colgroup, atom.Table, atom.Tbody,
		atom.Td, atom.Tfoot, atom.Th, atom.Thead, atom.Tr,
		atom.Details, atom.Summary, atom.Img,
		atom.Big, atom.Center, atom.Font, atom.Nobr, atom.Strike, atom.Tt)

	// dropped elements are removed with their content: the tokenizer
	// reads the content of the first ones as text, which is code or is
	// not shown; svg and math are foreign content, where a browser parses
	// markup differently from HTML, the usual way past a sanitizer
	// (mutation XSS); template and select hold content a reader does not
	// see as text.
	dropped = set(atom.Script, atom.Title, atom.Textarea, atom.Xmp,
		atom.Plaintext, atom.Noscript, atom.Noembed, atom.Noframes,
		atom.Iframe,
		atom.Svg, atom.Math,
		atom.Template, atom.Select)

	// foreign elements end at a self-closing tag (HTML Standard 13.2.6.5),
	// unlike HTML elements, which ignore its slash.
	foreign = set(atom.Svg, atom.Math)

	// backgrounds are the elements whose background attribute shows an
	// image (HTML Standard 15.3.10).
	backgrounds = set(atom.Body, atom.Table, atom.Thead, atom.Tbody,
		atom.Tfoot, atom.Tr, atom.Td, atom.Th)
)

// attributes are the attributes [Sanitize] copies as they are, from any
// kept element: text direction and language, accessibility, the class
// and id a style sheet selects by, and the presentational attributes of
// tables, images, lists, and font that mail uses for layout. aria-*
// attributes are copied too.
var attributes = map[string]bool{
	"abbr": true, "align": true, "alink": true, "alt": true, "bgcolor": true,
	"border": true, "cellpadding": true, "cellspacing": true, "class": true,
	"clear": true, "color": true, "colspan": true, "datetime": true,
	"dir": true, "face": true, "frame": true, "headers": true, "height": true,
	"hidden": true, "hspace": true, "id": true, "lang": true, "link": true,
	"noshade": true, "nowrap": true, "open": true, "reversed": true,
	"role": true, "rowspan": true, "rules": true, "scope": true, "size": true,
	"span": true, "start": true, "summary": true, "text": true, "title": true,
	"type": true, "valign": true, "value": true, "vlink": true, "vspace": true,
	"width": true,
}

// Sanitize writes to w a copy of the HTML document read from r that is
// safe to show, as policy allows, and returns what it found. The copy
// keeps the elements, attributes, and CSS a message's layout needs and
// no scripts, forms, embedded content, or comments. Links keep only
// http, https, mailto, and tel URLs and open in a new tab. Images keep
// cid: URLs naming an inline part of policy, rewritten to the part's
// URL, and remote http and https URLs only under [RemoteAllowed]. CSS
// keeps what Gmail supports, without @import or @font-face. It returns
// r's or w's error.
//
// DESIGN: It reads the document once, token by token, and writes each
// token it keeps again itself: a known element, its allowed attributes
// quoted and escaped, escaped text, and CSS it filtered without a < in
// it, each CSS token apart from the next. A browser therefore reads the
// copy as the same tokens, whatever tree it builds of them, and never
// enters a state where text becomes markup: no foreign content, and no
// raw text but its CSS. It does not build the document's tree with
// [html.Parse], whose error recovery lets a crafted document of a few
// kilobytes take hundreds of megabytes; a sender writes the HTML.
// bluemonday, which reads tokens too, applies one URL rule to links and
// images alike, so it cannot block remote images while keeping links,
// reports nothing of what it drops, keeps no <style> element, and cannot
// rewrite a url() in CSS.
func Sanitize(w io.Writer, r io.Reader, policy Policy) (Sanitized, error) {
	s := sanitizer{
		policy:  policy,
		out:     bufio.NewWriter(w),
		inline:  map[string]string{},
		shown:   map[string]bool{},
		cssLeft: maxCSS,
	}
	for _, part := range policy.Inline {
		// The first part with a Content-ID wins, as mail clients do.
		if _, ok := s.inline[part.ContentID]; part.ContentID != "" && !ok {
			s.inline[part.ContentID] = part.ID
		}
	}
	s.out.WriteString(doctype)
	if err := s.copy(html.NewTokenizer(r)); err != nil {
		return Sanitized{}, err
	}
	// A bufio.Writer keeps its first write error and returns it here.
	if err := s.out.Flush(); err != nil {
		return Sanitized{}, err
	}
	return s.found, nil
}

// sanitizer holds the state of one [Sanitize] call.
type sanitizer struct {
	policy Policy
	out    *bufio.Writer
	inline map[string]string // part IDs by Content-ID
	shown  map[string]bool   // part IDs in found.Shown
	found  Sanitized

	// skipping is the dropped element whose content is being skipped,
	// and depth how many of it are open.
	skipping atom.Atom
	depth    int
	// style is whether the next text is a style element's content, and
	// media that element's media queries.
	style bool
	media []byte
	// cssLeft is how many bytes of CSS the document may still have read.
	cssLeft int
}

// spend reports whether css, a style sheet or style attribute, fits in
// what is left of [maxCSS], and takes its size from it if so.
func (s *sanitizer) spend(css []byte) bool {
	if len(css) > s.cssLeft {
		return false
	}
	s.cssLeft -= len(css)
	return true
}

// copy writes the tokens of tokens that [Sanitize] keeps to s.out. It
// returns the tokenizer's read error.
func (s *sanitizer) copy(tokens *html.Tokenizer) error {
	for {
		kind := tokens.Next()
		switch kind {
		// The end of the input, or a read error to return.
		case html.ErrorToken:
			if err := tokens.Err(); err != io.EOF {
				return err
			}
			return nil
		// Text, or a raw text element's whole content, e.g. a style sheet.
		case html.TextToken:
			s.text(tokens.Text())
		// <p> or <br/>; the slash matters only to foreign elements.
		case html.StartTagToken, html.SelfClosingTagToken:
			s.start(kind, tokens)
		// </p>, written only if its element is kept.
		case html.EndTagToken:
			name, _ := tokens.TagName()
			s.end(atom.Lookup(name))
		default:
			// Comments, including Outlook's conditional ones, and the
			// sender's doctype are dropped.
		}
	}
}

// text writes text, a text token's content.
func (s *sanitizer) text(text []byte) {
	switch {
	// Inside a dropped element: its content goes with it.
	case s.depth > 0:
	// The tokenizer reads a style element's content as one text.
	case s.style:
		s.style = false
		if !s.spend(text) {
			return
		}
		if css := s.stylesheet(text); len(css) > 0 {
			s.out.WriteString("<style")
			if len(s.media) > 0 {
				s.attr(attrMedia, string(s.media))
			}
			s.out.WriteString(">")
			s.out.Write(css)
			s.out.WriteString("</style>")
		}
	// Text, escaped, so no < or & in it starts markup.
	default:
		s.out.WriteString(html.EscapeString(string(text)))
	}
}

// attr writes the attribute name with value, quoted and escaped, inside
// a start tag.
func (s *sanitizer) attr(name, value string) {
	s.out.WriteString(" ")
	s.out.WriteString(name)
	s.out.WriteString(`="`)
	s.out.WriteString(html.EscapeString(value))
	s.out.WriteString(`"`)
}

// start writes the start tag of kind that tokens is at, if its element
// is kept, with the attributes kept.
func (s *sanitizer) start(kind html.TokenType, tokens *html.Tokenizer) {
	name, _ := tokens.TagName()
	element := atom.Lookup(name)
	// A foreign element's self-closing tag holds no content.
	empty := kind == html.SelfClosingTagToken && foreign[element]
	switch {
	// Inside a dropped element: one nested in it with its name must end
	// before the dropped one's own end tag ends the skipping.
	case s.depth > 0:
		if element == s.skipping && !empty {
			s.depth++
		}
	// Dropped with its content, until its end tag.
	case dropped[element]:
		if !empty {
			s.skipping, s.depth = element, 1
		}
	// Written with its content, by text, with its media queries only; a
	// style sheet a screen would not apply is dropped instead.
	case element == atom.Style:
		media, ok := styleMedia(tokens)
		if !ok {
			s.skipping, s.depth = element, 1
			return
		}
		s.style, s.media = true, media
	// Copied with the attributes it may keep.
	case kept[element]:
		s.tag(element, tokens)
	// Unwrapped: the tag goes, its content stays.
	default:
	}
}

// styleMedia returns the media queries of the style element whose start
// tag tokens is at, as [mediaList] keeps them, and whether a browser
// applies the style sheet to a screen: its type, if any, is text/css, and
// its media, if any, keeps a query (HTML Standard 4.2.6). It reads the
// tag's attributes.
func styleMedia(tokens *html.Tokenizer) ([]byte, bool) {
	var media []byte
	for name, value := range attrs(tokens) {
		value = strings.TrimSpace(value)
		switch {
		// e.g. type="text/plain", which a browser does not read as CSS.
		case name == attrType && value != "" && strings.ToLower(value) != mediaCSS:
			return nil, false
		// e.g. media="print", for other media than a screen, or a query
		// with a feature Gmail does not support.
		case name == attrMedia && value != "":
			media = mediaList(value)
			if media == nil {
				return nil, false
			}
		}
	}
	return media, true
}

// attrs yields the attributes of the tag tokens is at, as the tokenizer
// gives them: names in lower case, values with entities decoded, and
// only the first of two attributes with one name, as a browser keeps it
// (HTML Standard 13.2.5.33).
func attrs(tokens *html.Tokenizer) iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for {
			// TagAttr returns no key for a tag without attributes.
			key, value, more := tokens.TagAttr()
			if key != nil && !yield(string(key), string(value)) {
				return
			}
			if !more {
				return
			}
		}
	}
}

// tag writes the start tag of element, a kept element, that tokens is
// at, with the attributes kept.
func (s *sanitizer) tag(element atom.Atom, tokens *html.Tokenizer) {
	s.out.WriteString("<")
	s.out.WriteString(element.String())
	hasLink := false
	for name, raw := range attrs(tokens) {
		value, ok := s.attribute(element, name, raw)
		if !ok {
			continue
		}
		hasLink = hasLink || name == attrHref
		s.attr(name, value)
	}
	if element == atom.A && hasLink {
		s.out.WriteString(linkTarget)
	}
	s.out.WriteString(">")
}

// attribute returns the value to write for element's attribute name
// with value, or false to drop it.
func (s *sanitizer) attribute(element atom.Atom, name, value string) (string, bool) {
	switch {
	// A link, kept only as an absolute http, https, mailto, or tel URL,
	// and only on a, the one kept element that follows it.
	case name == attrHref && element == atom.A:
		return link(value)
	// An image, from the attribute of an element that loads one; on any
	// other element it shows nothing, so its part must stay a download.
	case name == attrSrc && element == atom.Img, name == attrBackground && backgrounds[element]:
		img := s.image(value)
		s.note(img)
		return img.url, img.url != ""
	// Inline CSS, as far as [sanitizer.declarations] keeps it.
	case name == attrStyle:
		if !s.spend([]byte(value)) {
			return "", false
		}
		css := s.declarations(value)
		return string(css), len(css) > 0
	// Layout, text, and accessibility attributes, copied; [sanitizer.tag]
	// escapes the value.
	case attributes[name], isAria(name):
		return value, true
	// Event handlers, data-*, form and embedding attributes, and the rest.
	default:
		return "", false
	}
}

// isAria reports whether name is an aria-* attribute written in lower
// case letters and hyphens only, so it is safe to write unquoted.
func isAria(name string) bool {
	rest, ok := strings.CutPrefix(name, attrAria)
	if !ok || rest == "" {
		return false
	}
	return strings.Trim(rest, "abcdefghijklmnopqrstuvwxyz-") == ""
}

// end writes the end tag of element, if kept.
func (s *sanitizer) end(element atom.Atom) {
	switch {
	// Inside a dropped element: its own end tag closes one of it.
	case s.depth > 0:
		if element == s.skipping {
			s.depth--
		}
	// text wrote the style element whole, end tag included.
	case element == atom.Style:
		s.style = false
	case kept[element]:
		s.out.WriteString("</")
		s.out.WriteString(element.String())
		s.out.WriteString(">")
	// An unwrapped element's end tag goes, as its start tag did.
	default:
	}
}

// link returns value, a link's URL, as written again, if it is an
// absolute http, https, mailto, or tel URL.
func link(value string) (string, bool) {
	u, ok := absolute(value)
	if !ok {
		return "", false
	}
	switch u.Scheme {
	// A web link needs a host; "http:x" has none.
	case schemeHTTP, schemeHTTPS:
		return u.String(), u.Host != ""
	// An address or a number, the URL's opaque part.
	case schemeMailto, schemeTel:
		return u.String(), u.Opaque != ""
	// javascript:, data:, file:, and any other scheme.
	default:
		return "", false
	}
}

// imageURL is what [sanitizer.image] makes of an image's URL.
type imageURL struct {
	url     string // the URL to write, or "" to drop it
	part    string // the ID of the inline part it shows, or ""
	blocked bool   // whether it is a remote URL [RemoteBlocked] dropped
}

// image returns what to write for value, an image's URL: an inline
// part's URL for a cid: URL naming one of [Policy.Inline], or a remote
// http or https URL as written again, under [RemoteAllowed]. It changes
// nothing in s.found; [sanitizer.note] does, once the URL is written.
func (s *sanitizer) image(value string) imageURL {
	value = trim(value)
	if len(value) >= len(schemeCID) && strings.EqualFold(value[:len(schemeCID)], schemeCID) {
		return s.part(value[len(schemeCID):])
	}
	u, ok := absolute(value)
	if !ok || (u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS) || u.Host == "" {
		return imageURL{}
	}
	if s.policy.Remote != RemoteAllowed {
		return imageURL{blocked: true}
	}
	return imageURL{url: u.String()}
}

// part returns the [imageURL] of the inline part whose Content-ID is id, a
// cid: URL's text after the scheme.
func (s *sanitizer) part(id string) imageURL {
	// A cid: URL percent-encodes the Content-ID (RFC 2392, section 2).
	decoded, err := url.PathUnescape(id)
	if err == nil {
		id = decoded
	}
	partID, ok := s.inline[id]
	if !ok || s.policy.PartURL == nil {
		return imageURL{}
	}
	return imageURL{url: s.policy.PartURL(partID), part: partID}
}

// note records in s.found the inline part img shows, or that img is a
// remote image left out.
func (s *sanitizer) note(img imageURL) {
	if img.blocked {
		s.found.Blocked = true
	}
	if img.part != "" && !s.shown[img.part] {
		s.shown[img.part] = true
		s.found.Shown = append(s.found.Shown, img.part)
	}
}

// absolute returns value parsed as a URL with a scheme, or false. A
// relative URL would resolve against inbok's own origin.
func absolute(value string) (*url.URL, bool) {
	value = trim(value)
	// A scheme-relative URL, e.g. //cdn.example/a.png, takes the scheme
	// of the page, which inbok serves over https.
	if strings.HasPrefix(value, "//") {
		value = schemeHTTPS + ":" + value
	}
	// A URL with a space in it is dropped, as bluemonday drops it, rather
	// than encoded. url.Parse refuses the control characters a browser
	// would remove inside a URL, e.g. a tab in "java\tscript:", so such a
	// URL is dropped rather than read as the browser reads it.
	if strings.ContainsRune(value, ' ') {
		return nil, false
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" {
		return nil, false
	}
	return u, true
}

// trim returns value without the leading and trailing C0 controls and
// spaces a browser removes from a URL (WHATWG URL Standard, 4.4).
func trim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool { return r <= ' ' })
}
